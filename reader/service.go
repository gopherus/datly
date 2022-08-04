package reader

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/viant/datly/shared"
	"github.com/viant/datly/view"
	"github.com/viant/gmetric/counter"
	"github.com/viant/sqlx/io"
	"github.com/viant/sqlx/io/read"
	"github.com/viant/sqlx/io/read/cache"
	"github.com/viant/sqlx/option"
	"sync"
	"time"
)

//Service represents reader service
type Service struct {
	sqlBuilder *Builder
	Resource   *view.Resource
}

//Read select view from database based on View and assign it to dest. ParentDest has to be pointer.
func (s *Service) Read(ctx context.Context, session *Session) error {
	var err error
	if err = session.Init(); err != nil {
		return err
	}

	wg := sync.WaitGroup{}

	collector := session.View.Collector(session.Dest, session.View.MatchStrategy.SupportsParallel())
	errors := shared.NewErrors(0)
	s.readAll(ctx, session, collector, &wg, errors)
	wg.Wait()
	err = errors.Error()
	if err != nil {
		return err
	}
	collector.MergeData()

	if err = errors.Error(); err != nil {
		return err
	}

	if dest, ok := session.Dest.(*interface{}); ok {
		*dest = collector.Dest()
	}
	return nil
}

func (s *Service) afterRead(session *Session, collector *view.Collector, start *time.Time, err error, onFinish counter.OnDone) {
	end := time.Now()
	viewName := collector.View().Name
	session.View.Logger.ReadTime(viewName, start, &end, err)
	//TODO add to metrics record read
	elapsed := end.Sub(*start)

	session.AddMetric(&Metric{View: viewName, ElapsedMs: int(elapsed.Milliseconds()), Elapsed: elapsed.String(), Rows: collector.Len()})
	if err != nil {
		session.View.Counter.IncrementValue(Error)
	} else {
		session.View.Counter.IncrementValue(Success)
	}
	onFinish(end)
}

func (s *Service) readAll(ctx context.Context, session *Session, collector *view.Collector, wg *sync.WaitGroup, errorCollector *shared.Errors) {
	start := time.Now()
	onFinish := session.View.Counter.Begin(start)
	defer s.afterRead(session, collector, &start, errorCollector.Error(), onFinish)

	var collectorFetchEmitted bool
	defer s.afterReadAll(collectorFetchEmitted, collector)

	aView := collector.View()
	selector := session.Selectors.Lookup(aView)
	collectorChildren := collector.Relations(selector)
	wg.Add(len(collectorChildren))

	relationGroup := sync.WaitGroup{}
	if !collector.SupportsParallel() {
		relationGroup.Add(len(collectorChildren))
	}
	for i := range collectorChildren {
		go func(i int) {
			defer s.afterRelationCompleted(wg, collector, &relationGroup)
			s.readAll(ctx, session, collectorChildren[i], wg, errorCollector)
		}(i)
	}

	collector.WaitIfNeeded()
	batchData := s.batchData(collector)
	if batchData.ColumnName != "" && len(batchData.Values) == 0 {
		return
	}

	db, err := aView.Db(ctx)
	if err != nil {
		errorCollector.Append(err)
		return
	}

	session.View.Counter.IncrementValue(Pending)
	defer session.View.Counter.DecrementValue(Pending)
	err = s.exhaustRead(ctx, aView, selector, batchData, db, collector, session)
	if err != nil {
		errorCollector.Append(err)
	}
	if collector.SupportsParallel() {
		return
	}

	collectorFetchEmitted = true
	collector.Fetched()

	relationGroup.Wait()
	ptr, xslice := collector.Slice()
	for i := 0; i < xslice.Len(ptr); i++ {
		if actual, ok := xslice.ValuePointerAt(ptr, i).(OnRelationer); ok {
			actual.OnRelation(ctx)
			continue
		}

		break
	}
}

func (s *Service) afterRelationCompleted(wg *sync.WaitGroup, collector *view.Collector, relationGroup *sync.WaitGroup) {
	wg.Done()
	if collector.SupportsParallel() {
		return
	}
	relationGroup.Done()
}

func (s *Service) afterReadAll(collectorFetchEmitted bool, collector *view.Collector) {
	if collectorFetchEmitted {
		return
	}
	collector.Fetched()
}

func (s *Service) batchData(collector *view.Collector) *view.BatchData {
	batchData := &view.BatchData{}

	batchData.Values, batchData.ColumnName = collector.ParentPlaceholders()
	batchData.ParentReadSize = len(batchData.Values)

	return batchData
}

func (s *Service) exhaustRead(ctx context.Context, view *view.View, selector *view.Selector, batchData *view.BatchData, db *sql.DB, collector *view.Collector, session *Session) error {
	batchData.ValuesBatch, batchData.Parent = sliceWithLimit(batchData.Values, batchData.Parent, batchData.Parent+view.Batch.Parent)
	visitor := collector.Visitor(ctx)

	for {
		fullMatch, smartMatch, err := s.getMatchers(view, selector, batchData, collector, session)
		if err != nil {
			return err
		}

		err = s.query(ctx, view, db, collector, visitor, fullMatch, smartMatch)
		if err != nil {
			return err
		}

		if batchData.Parent == batchData.ParentReadSize {
			break
		}

		var nextParents int
		batchData.ValuesBatch, nextParents = sliceWithLimit(batchData.Values, batchData.Parent, batchData.Parent+view.Batch.Parent)
		batchData.Parent += nextParents
	}
	return nil
}

func (s *Service) getMatchers(aView *view.View, selector *view.Selector, batchData *view.BatchData, collector *view.Collector, session *Session) (fullMatch *cache.SmartMatcher, smartMatch *cache.SmartMatcher, err error) {
	wg := &sync.WaitGroup{}
	wg.Add(2)

	var fullMatchErr, smartMatchErr error
	go func() {
		defer wg.Done()

		fullMatch, fullMatchErr = s.sqlBuilder.Build(aView, selector, batchData, collector.Relation(), nil, session.Parent)
	}()

	go func() {
		defer wg.Done()

		if aView.Cache != nil && aView.Cache.Warmup != nil {
			smartMatch, smartMatchErr = s.sqlBuilder.Build(aView, selector, batchData, collector.Relation(), &Exclude{Pagination: true, ColumnsIn: true}, session.Parent)
		}
	}()

	wg.Wait()
	return fullMatch, smartMatch, notNilErr(fullMatchErr, smartMatchErr)
}

func notNilErr(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *Service) query(ctx context.Context, aView *view.View, db *sql.DB, collector *view.Collector, visitor view.Visitor, fullMatcher, smartMatcher *cache.SmartMatcher) error {
	begin := time.Now()

	var options = []option.Option{io.Resolve(collector.Resolve)}
	if aView.Cache != nil {
		service, err := aView.Cache.Service()
		if err != nil {
			return err
		}

		options = append(options, service)
	}

	reader, err := read.New(ctx, db, fullMatcher.RawSQL, collector.NewItem(), options...)
	if err != nil {
		aView.Logger.LogDatabaseErr(err)
		return fmt.Errorf("database error occured while fetching data for view %v", aView.Name)
	}

	defer func() {
		stmt := reader.Stmt()
		if stmt == nil {
			return
		}
		stmt.Close()
	}()
	readData := 0
	err = reader.QueryAll(ctx, func(row interface{}) error {
		row, err = aView.UnwrapDatabaseType(ctx, row)
		if err != nil {
			return err
		}

		readData++
		if fetcher, ok := row.(OnFetcher); ok {
			if err = fetcher.OnFetch(ctx); err != nil {
				return err
			}
		}
		return visitor(row)
	}, smartMatcher, fullMatcher.RawArgs...)
	end := time.Now()
	aView.Logger.ReadingData(end.Sub(begin), fullMatcher.RawSQL, readData, fullMatcher.RawArgs, err)
	if err != nil {
		aView.Logger.LogDatabaseErr(err)
		return fmt.Errorf("database error occured while fetching data for view %v", aView.Name)
	}

	return nil
}

//New creates Service instance
func New() *Service {
	return &Service{
		sqlBuilder: NewBuilder(),
		Resource:   view.EmptyResource(),
	}
}

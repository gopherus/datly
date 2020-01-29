package reader

import (
	"context"
	"fmt"
	"github.com/pkg/errors"
	"github.com/viant/datly/base"
	"github.com/viant/datly/base/contract"
	"github.com/viant/datly/config"
	"github.com/viant/datly/data"
	"github.com/viant/datly/generic"
	"github.com/viant/datly/metric"
	"github.com/viant/datly/shared"
	"github.com/viant/dsc"
	"github.com/viant/toolbox"
	"sync"
)

//Service represents a reader service
type Service interface {
	Read(ctx context.Context, request *Request) *Response
}

type service struct {
	base.Service
}

//Read reads data for matched request Path
func (s *service) Read(ctx context.Context, request *Request) *Response {
	response := NewResponse()
	defer response.OnDone()
	if shared.IsLoggingEnabled() {
		toolbox.Dump(request)
	}
	err := s.read(ctx, request, response)
	if err != nil {
		response.AddError(shared.ErrorTypeException, "service.Read", err)
	}
	if shared.IsLoggingEnabled() {
		toolbox.Dump(response)
	}
	return response
}

func (s *service) read(ctx context.Context, req *Request, resp *Response) error {
	rule, err := s.Match(ctx, &req.Request, &resp.Response)
	if rule == nil {
		return err
	}
	waitGroup := &sync.WaitGroup{}
	waitGroup.Add(len(rule.Output))

	for i := range rule.Output {
		go func(io *data.IO) {
			defer waitGroup.Done()
			err := s.readOutputData(ctx, rule, io, req, resp)
			if err != nil {
				resp.AddError(shared.ErrorTypeException, "service.readOutputData", err)
			}
		}(rule.Output[i])
	}
	waitGroup.Wait()
	return nil
}

func (s *service) readOutputData(ctx context.Context, rule *config.Rule, io *data.IO, request *Request, response *Response) error {
	view, err := rule.View(io.DataView)
	if err != nil {
		return err
	}
	selector := view.Selector.Clone()
	genericProvider := generic.NewProvider()
	collection := genericProvider.NewSlice()
	selector.CaseFormat = io.CaseFormat
	err = s.readViewData(ctx, collection, selector, view, rule, request, response)
	if err == nil {
		io.SetOutput(collection, response)
	}
	return err
}

func (s *service) readViewData(ctx context.Context, collection generic.Collection, selector *data.Selector, view *data.View, rule *config.Rule, request *Request, response *Response) error {
	dataPool, err := s.BuildDataPool(ctx, request.Request, view, rule, response.Metrics)
	if err != nil {
		return errors.Wrapf(err, "failed to assemble bindingData with rule: %v", rule.Info.URL)
	}
	selector.Apply(dataPool)
	waitGroup := &sync.WaitGroup{}
	waitGroup.Add(1 + len(view.Refs))
	refData := &contract.Data{}
	go s.readRefs(ctx, view, selector, dataPool, rule, request, response, waitGroup, refData)
	SQL, parameters, err := view.BuildSQL(selector, dataPool)
	if err != nil {
		return errors.Wrapf(err, "failed to build FromFragments with rule: %v", rule.Info.URL)
	}

	if shared.IsLoggingEnabled() {
		fmt.Printf("=====SQL======\n%v, \nparams: %v, dataPool: %+v\n", SQL, parameters, dataPool)
	}
	parametrizedSQL := &dsc.ParametrizedSQL{SQL: SQL, Values: parameters}
	query := metric.NewQuery(parametrizedSQL)

	err = s.readData(ctx, SQL, parameters, view.Connector, func(record data.Record) error {
		query.Increment()
		collection.Add(record)
		return nil
	})
	query.SetFetchTime()
	response.Metrics.AddQuery(query)
	if err != nil {
		return errors.Wrapf(err, "failed to read data with rule: %v", rule.Info.URL)
	}
	if selector.CaseFormat != view.CaseFormat {
		collection.Proto().OutputCaseFormat(view.CaseFormat, selector.CaseFormat)
	}
	waitGroup.Wait()
	if len(refData.Data) > 0 {
		s.assignRefs(view, collection, refData.Data)
	}
	if view.OnRead != nil {
		collection.Objects(func(item *generic.Object) (toContinue bool, err error) {
			return view.OnRead.Visit(ctx, view, item)
		})
	}
	return err
}

func (s *service) readData(ctx context.Context, SQL string, parameters []interface{}, connector string, onRecord func(record data.Record) error) error {
	manager, err := s.Manager(ctx, connector)
	if err != nil {
		return err
	}
	return manager.ReadAllWithHandler(SQL, parameters, func(scanner dsc.Scanner) (toContinue bool, err error) {
		record := map[string]interface{}{}
		err = scanner.Scan(&record)
		if err == nil {
			err = onRecord(record)
		}
		return err == nil, err
	})
}

func (s *service) readRefs(ctx context.Context, owner *data.View, selector *data.Selector, bindings map[string]interface{}, rule *config.Rule, request *Request, response *Response, group *sync.WaitGroup, refData *contract.Data) {
	defer group.Done()
	refs := owner.Refs
	if len(refs) == 0 {
		return
	}

	for i, ref := range refs {
		if !selector.IsSelected(ref.Columns()) { //when selector comes with columns, make sure that reference is within that list.
			group.Done()
			continue
		}
		go s.readRefData(ctx, owner, refs[i], selector, bindings, response, rule, request, refData, group)
	}
}

func (s *service) readRefData(ctx context.Context, owner *data.View, ref *data.Reference, selector *data.Selector, bindings map[string]interface{}, response *Response, rule *config.Rule, request *Request, refData *contract.Data, group *sync.WaitGroup) {
	defer group.Done()
	view, err := s.buildRefView(owner.Clone(), ref, selector, bindings)
	if err != nil {
		response.AddError(shared.ErrorTypeException, "service.readOutputData", err)
		return
	}
	provider := generic.NewProvider()
	var collection generic.Collection
	if ref.Cardinality == shared.CardinalityOne {
		collection = provider.NewMap(ref.RefIndex())
	} else {
		collection = provider.NewMultimap(ref.RefIndex())
	}
	refViewSelector := view.Selector.Clone()
	if refViewSelector.CaseFormat == "" {
		refViewSelector.CaseFormat = selector.CaseFormat
	}
	err = s.readViewData(ctx, collection, refViewSelector, view, rule, request, response)
	if err != nil {
		response.AddError(shared.ErrorTypeException, "service.readViewData", err)
	}
	refData.Put(ref.Name, collection)
}

func (s *service) buildRefView(owner *data.View, ref *data.Reference, selector *data.Selector, bindings map[string]interface{}) (*data.View, error) {
	refView := ref.View()
	if refView == nil {
		return nil, errors.Errorf("ref view was empty for owner: %v", owner.Name)
	}
	refView = refView.Clone()
	//Only when owner and reference connector is the same you can apply JOIN, otherwise all reference table has to be read into memory.
	if refView.Connector == owner.Connector {
		selector = selector.Clone()
		selector.Columns = ref.Columns()
		SQL, parameters, err := owner.BuildSQL(selector, bindings)
		if err != nil {
			return nil, err
		}
		refView.Params = parameters
		join := &data.Join{
			Type:  shared.JoinTypeInner,
			Alias: ref.Alias(),
			Table: fmt.Sprintf("(%s)", SQL),
			On:    ref.Criteria(refView.Alias),
		}
		refView.AddJoin(join)
	}
	return refView, nil
}

func (s *service) assignRefs(owner *data.View, ownerCollection generic.Collection, refData map[string]generic.Collection) error {
	return ownerCollection.Objects(func(item *generic.Object) (b bool, err error) {
		for _, ref := range owner.Refs {
			if owner.HideRefIDs {
				for _, column := range ref.Columns() {
					ownerCollection.Proto().Hide(column)
				}
			}
			data, ok := refData[ref.Name]
			if !ok {
				continue
			}
			index := ref.Index()
			key := index(item)

			if ref.Cardinality == shared.CardinalityOne {
				aMap, ok := data.(*generic.Map)
				if !ok {
					return false, errors.Errorf("invalid collection: expected : %T, but had %T", aMap, data)
				}
				value := aMap.Object(key)
				item.SetValue(ref.Name, value)
			} else {
				aMultimap, ok := data.(*generic.Multimap)
				if !ok {
					return false, errors.Errorf("invalid collection: expected : %T, but had %T", aMultimap, data)
				}
				value := aMultimap.Slice(key)
				item.SetValue(ref.Name, value)
			}
		}
		return true, nil
	})
}

//New creates a service
func New(ctx context.Context, config *config.Config) (Service, error) {
	baseService, err := base.New(ctx, config)
	if err != nil {
		return nil, err
	}
	return &service{
		Service: baseService,
	}, err
}

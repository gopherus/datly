package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/viant/afs"
	"github.com/viant/afs/cache"
	"github.com/viant/afs/file"
	furl "github.com/viant/afs/url"
	"github.com/viant/cloudless/resource"
	"github.com/viant/datly/auth/secret"
	"github.com/viant/datly/codec"
	"github.com/viant/datly/router"
	"github.com/viant/datly/view"
	"github.com/viant/gmetric"
	"net/http"
	"strings"
	"sync"
	"time"
)

type (
	Service struct {
		Config               *Config
		visitors             codec.Visitors
		types                view.Types
		mux                  sync.RWMutex
		routersIndex         map[string]*router.Router
		fs                   afs.Service
		cfs                  afs.Service //cache file system
		routeResourceTracker *resource.Tracker
		dataResourceTracker  *resource.Tracker
		dataResourcesIndex   map[string]*view.Resource
		metrics              *gmetric.Service
		mainRouter           *Router
		cancelFn             context.CancelFunc
		session              *Session
	}
)

func (r *Service) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	aRouter, ok := r.Router()
	if !ok {
		writer.WriteHeader(http.StatusNotFound)
	}

	aRouter.Handle(writer, request)
}

func (r *Service) Router() (*Router, bool) {
	mainRouter := r.mainRouter
	return mainRouter, mainRouter != nil
}

func (r *Service) Close() error {
	if r.cancelFn != nil {
		r.cancelFn()
	}

	return nil
}

//New creates gateway Service. It is important to call Service.Close before Service got Garbage collected.
func New(ctx context.Context, config *Config, statusHandler http.Handler, authorizer Authorizer, visitors codec.Visitors, types view.Types, metrics *gmetric.Service) (*Service, error) {
	config.Init()
	err := config.Validate()
	if err != nil {
		return nil, err
	}

	URL, _ := furl.Split(config.RouteURL, file.Scheme)
	cfs := cache.Singleton(URL)

	srv := &Service{
		visitors:             visitors,
		metrics:              metrics,
		types:                types,
		Config:               config,
		mux:                  sync.RWMutex{},
		fs:                   afs.New(),
		cfs:                  cfs,
		dataResourcesIndex:   map[string]*view.Resource{},
		routeResourceTracker: resource.New(config.RouteURL, time.Duration(config.SyncFrequencyMs)*time.Millisecond),
		dataResourceTracker:  resource.New(config.DependencyURL, time.Duration(config.SyncFrequencyMs)*time.Millisecond),
		routersIndex:         map[string]*router.Router{},
		mainRouter:           NewRouter(map[string]*router.Router{}, config, metrics, statusHandler, authorizer),
		session:              NewSession(config.ChangeDetection),
	}

	if err = initSecrets(ctx, config); err != nil {
		return nil, err
	}

	err = srv.createRouterIfNeeded(ctx, metrics, statusHandler, authorizer)
	srv.detectChanges(metrics, statusHandler, authorizer)

	return srv, err
}

func (r *Service) createRouterIfNeeded(ctx context.Context, metrics *gmetric.Service, statusHandler http.Handler, authorizer Authorizer) error {
	defer func() {
		if r.session == nil {
			return
		}

		r.session.UpdateFailureCounter()
	}()

	if r.session == nil {
		r.session = NewSession(r.Config.ChangeDetection)
	}

	fs := r.reloadFs()
	resources, changed, err := r.getDataResources(ctx, fs)
	if err != nil {
		return err
	}

	routers, changed, err := r.getRouters(ctx, fs, resources, changed)
	if err != nil || !changed {
		return err
	}

	mainRouter := NewRouter(routers, r.Config, metrics, statusHandler, authorizer)
	r.mux.Lock()
	r.mainRouter = mainRouter
	r.routersIndex = routers
	r.dataResourcesIndex = resources
	r.session = nil
	r.mux.Unlock()

	return nil
}

func (r *Service) getRouters(ctx context.Context, fs afs.Service, resources map[string]*view.Resource, viewResourcesChanged bool) (routers map[string]*router.Router, changed bool, err error) {
	updatedMap, removedMap, err := r.detectRoutersChanges(ctx, fs)
	if err != nil {
		return nil, false, err
	}

	if !viewResourcesChanged && len(updatedMap) == 0 && len(removedMap) == 0 {
		return r.routersIndex, false, nil
	}

	routers = map[string]*router.Router{}
	for routerURL := range r.routersIndex {
		if (updatedMap[routerURL] || removedMap[routerURL]) && !changed {
			continue
		}

		routers[routerURL] = r.routersIndex[routerURL]
	}

	routersChan := make(chan func() (*router.Resource, string, error))
	channelSize := r.populateRoutersChan(ctx, routersChan, updatedMap, fs, resources)
	counter := 0
	var errors []error
	for fn := range routersChan {
		routerResource, URL, err := fn()
		if err != nil {
			errors = append(errors, err)
		} else {
			routers[URL] = router.New(routerResource, router.ApiPrefix(r.Config.APIPrefix))
		}

		counter++
		if counter >= channelSize {
			close(routersChan)
		}
	}

	if err := r.combineErrors("routers", errors); err != nil {
		return nil, false, err
	}

	return routers, true, nil
}

func (r *Service) getDataResources(ctx context.Context, fs afs.Service) (resources map[string]*view.Resource, changed bool, err error) {
	updatedMap, removedMap, err := r.detectResourceChanges(ctx, fs)
	if err != nil {
		return nil, false, err
	}

	if len(updatedMap) == 0 && len(removedMap) == 0 {
		return copyResourcesMap(r.dataResourcesIndex), false, nil
	}

	result := map[string]*view.Resource{}
	for resourceURL, dataResource := range r.dataResourcesIndex {
		if updatedMap[dataResource.SourceURL] || removedMap[dataResource.SourceURL] {
			continue
		}

		result[resourceURL] = r.dataResourcesIndex[resourceURL]
	}

	resourceChan := make(chan func() (*view.Resource, string, error))
	channelSize := r.populateResourceChan(ctx, resourceChan, fs, updatedMap)
	counter := 0
	var errors []error
	for fn := range resourceChan {
		dependency, URL, err := fn()
		if err != nil {
			errors = append(errors, err)
		} else {
			result[URL] = dependency
		}

		counter++
		if counter >= channelSize {
			close(resourceChan)
		}
	}

	if err := r.combineErrors("dependencies", errors); err != nil {
		return nil, false, err
	}

	return result, true, nil
}

func (r *Service) combineErrors(resourceType string, errors []error) error {
	if len(errors) == 0 {
		return nil
	}

	actualErr := fmt.Errorf("failed to load %v due to the: %w", resourceType, errors[0])
	for i := 1; i < len(errors); i++ {
		actualErr = fmt.Errorf("%w, %v", actualErr, errors[i].Error())
	}

	return actualErr
}

func copyResourcesMap(index map[string]*view.Resource) map[string]*view.Resource {
	result := map[string]*view.Resource{}

	for key := range index {
		result[key] = index[key]
	}

	return result
}

func deepCopyResources(index map[string]*view.Resource) (map[string]*view.Resource, error) {
	marshal, err := json.Marshal(index)
	if err != nil {
		return nil, err
	}

	result := map[string]*view.Resource{}
	return result, json.Unmarshal(marshal, &result)
}

func (r *Service) populateResourceChan(ctx context.Context, resourceChan chan func() (*view.Resource, string, error), fs afs.Service, updatedResources map[string]bool) int {
	for resourceURL := range updatedResources {
		go func(URL string) {
			newResource, err := r.loadDependencyResource(URL, ctx, fs)
			resourceChan <- func() (*view.Resource, string, error) {
				return newResource, r.updateResourceKey(URL), err
			}
		}(resourceURL)
	}

	return len(updatedResources)
}

func (r *Service) loadDependencyResource(URL string, ctx context.Context, fs afs.Service) (*view.Resource, error) {
	dependency, ok := r.session.Dependencies[URL]
	if ok {
		return dependency, nil
	}

	var err error
	dependency, err = view.LoadResourceFromURL(ctx, URL, fs)
	return dependency, err
}

func (r *Service) detectResourceChanges(ctx context.Context, fs afs.Service) (map[string]bool, map[string]bool, error) {
	var updatedResources []string
	var removedResources []string

	err := r.dataResourceTracker.Notify(ctx, fs, func(URL string, operation resource.Operation) {
		if strings.Contains(URL, ".meta/") {
			return
		}

		switch operation {
		case resource.Added, resource.Modified:
			updatedResources = append(updatedResources, URL)
		case resource.Deleted:
			removedResources = append(removedResources, URL)
		}
	})

	if err != nil {
		return nil, nil, err
	}

	r.session.OnDependencyUpdated(updatedResources...)
	r.session.OnFileChange(removedResources...)
	return r.session.UpdatedDependencies, r.session.DeletedDependencies, err
}

func (r *Service) detectRoutersChanges(ctx context.Context, fs afs.Service) (map[string]bool, map[string]bool, error) {
	var updated []string
	var deleted []string
	err := r.routeResourceTracker.Notify(ctx, fs, func(URL string, operation resource.Operation) {
		if strings.Contains(URL, ".meta/") || !strings.HasSuffix(URL, ".yaml") {
			return
		}

		switch operation {
		case resource.Added, resource.Modified:
			updated = append(updated, URL)
		case resource.Deleted:
			deleted = append(deleted, URL)
		}
	})

	if err != nil {
		return nil, nil, err
	}

	r.session.OnRouterUpdated(updated...)
	r.session.OnRouterDeleted(deleted...)

	return r.session.UpdatedRouters, r.session.DeletedRouters, err
}

func (r *Service) detectChanges(metrics *gmetric.Service, statusHandler http.Handler, authorizer Authorizer) {
	ctx := context.Background()
	cancel, cancelFunc := context.WithCancel(ctx)
	r.cancelFn = cancelFunc
	go func() {
	outer:
		for {
			time.Sleep(r.Config.ChangeDetection._retry)
			select {
			case <-cancel.Done():
				break outer
			default:
				if err := r.createRouterIfNeeded(context.TODO(), metrics, statusHandler, authorizer); err != nil {
					fmt.Printf("error occured while recreating routers: %v \n", err.Error())
				}
			}
		}
	}()
}

func (r *Service) reloadFs() afs.Service {
	if r.Config.UseCacheFS {
		return r.cfs
	}
	return r.fs
}

func (r *Service) PreCachables(method string, uri string) ([]*view.View, error) {
	aRouter, ok := r.Router()
	if !ok {
		return []*view.View{}, nil
	}

	return aRouter.PreCacheables(method, uri)
}

func (r *Service) updateResourceKey(URL string) string {
	_, key := furl.Split(URL, file.Scheme)
	if index := strings.Index(key, "."); index != -1 {
		key = key[:index]
	}

	return key
}

func (r *Service) updateRouterAPIKeys(routes router.Routes) {
	for _, route := range routes {
		if route.APIKey == nil {
			route.APIKey = r.Config.APIKeys.Match(route.URI)
		}
	}
}

func (r *Service) populateRoutersChan(ctx context.Context, routersChan chan func() (*router.Resource, string, error), updatedMap map[string]bool, fs afs.Service, resources map[string]*view.Resource) int {
	for resourceURL := range updatedMap {
		go func(URL string) {
			routerResource, err := r.loadRouterResource(URL, resources, ctx, fs)
			routersChan <- func() (*router.Resource, string, error) {
				return routerResource, URL, err
			}
		}(resourceURL)
	}

	return len(updatedMap)
}

func (r *Service) loadRouterResource(URL string, resources map[string]*view.Resource, ctx context.Context, fs afs.Service) (*router.Resource, error) {
	routerResource, ok := r.session.Routers[URL]
	if ok {
		return routerResource, nil
	}

	copyResources, err := deepCopyResources(resources)
	if err != nil {
		return nil, err
	}

	routerResource, err = router.NewResourceFromURL(ctx, fs, URL, r.Config.Discovery(), r.visitors, r.types, r.metrics, copyResources)
	if err == nil {
		r.session.AddRouter(URL, routerResource)
	}

	return routerResource, err
}

func initSecrets(ctx context.Context, config *Config) error {
	if len(config.Secrets) == 0 {
		return nil
	}
	secrets := secret.New()
	for _, sec := range config.Secrets {
		if err := secrets.Apply(ctx, sec); err != nil {
			return err
		}
	}
	return nil
}

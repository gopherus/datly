package data

import (
	"context"
	"fmt"
	"github.com/viant/datly/shared"
	"github.com/viant/datly/sql"
	"github.com/viant/toolbox"
	rdata "github.com/viant/toolbox/data"
	"net/http"
	"reflect"
)

//Session groups data required to Read data
type Session struct {
	Dest          interface{} //slice
	View          *View
	Selectors     Selectors
	AllowUnmapped bool
	Subject       string
	HttpRequest   *http.Request
	MatchedPath   string

	pathVariables map[string]string
	cookies       map[string]string
	headers       map[string]string
	queryParams   map[string]string
}

//DataType returns Parent View.DataType
func (s *Session) DataType() reflect.Type {
	return s.View.DataType()
}

//NewReplacement creates parameter map common for all the views in session.
func (s *Session) NewReplacement(view *View) rdata.Map {
	aMap := rdata.NewMap()
	aMap.SetValue(string(shared.DataViewName), view.Name)
	aMap.SetValue(string(shared.SubjectName), s.Subject)

	return aMap
}

//Init initializes session
func (s *Session) Init(ctx context.Context, resource *Resource) error {
	var err error

	if err = s.View.Init(ctx, resource); err != nil {
		return err
	}

	s.Selectors.Init()

	if _, ok := s.Dest.(*interface{}); !ok {
		viewType := reflect.PtrTo(s.View.Schema.SliceType())
		destType := reflect.TypeOf(s.Dest)
		if viewType != destType {
			return fmt.Errorf("type mismatch, view slice type is: %v while destination type is %v", viewType.String(), destType.String())
		}
	}

	if s.HttpRequest != nil {
		uriParams, ok := toolbox.ExtractURIParameters(s.MatchedPath, s.HttpRequest.URL.Path)
		if !ok {
			return fmt.Errorf("route path doesn't match %v request URI %v", s.MatchedPath, s.HttpRequest.URL.Path)
		}

		if err = s.indexUriParams(uriParams); err != nil {
			return err
		}

		if err = s.indexCookies(); err != nil {
			return err
		}

		if err = s.indexHeaders(); err != nil {
			return err
		}

		if err = s.indexQueryParams(); err != nil {
			return err
		}
	}

	if err = s.isAnyRequiredParamMissing(); err != nil {
		return err
	}

	for _, selector := range s.Selectors {
		if selector.Criteria != nil {
			if _, err = sql.Parse([]byte(selector.Criteria.Expression)); err != nil {
				return err
			}
		}
	}

	return nil
}

//Header returns header value from http.Request bound with Session
func (s *Session) Header(name string) string {
	if s.HttpRequest == nil {
		return ""
	}

	headerValues := s.HttpRequest.Header[name]
	headerValue := ""
	if len(headerValues) > 0 {
		headerValue = headerValues[0]
	}

	return headerValue
}

//Cookie returns cookie value from http.Request bound with Session
func (s *Session) Cookie(name string) string {
	return s.cookies[name]
}

//PathVariable returns path variable from URL
func (s *Session) PathVariable(name string) string {
	return s.pathVariables[name]
}

func (s *Session) shouldIndexCookie(cookie *http.Cookie) bool {
	return s.View.shouldIndexCookie(cookie)
}

func (s *Session) indexCookies() error {
	s.cookies = make(map[string]string)
	cookies := s.HttpRequest.Cookies()
	for i := range cookies {
		if s.shouldIndexCookie(cookies[i]) {
			_, err := sql.Parse([]byte(cookies[i].Value))
			if err != nil {
				return err
			}
			s.cookies[cookies[i].Name] = cookies[i].Value
		}
	}
	return nil
}

func (s *Session) indexUriParams(params map[string]string) error {
	s.pathVariables = make(map[string]string)
	for key, val := range params {
		if s.View.shouldIndexUriParam(key) {
			_, err := sql.Parse([]byte(val))
			if err != nil {
				return err
			}
			s.pathVariables[key] = val
		}
	}
	return nil
}

func (s *Session) indexHeaders() error {
	s.headers = make(map[string]string)
	for key, val := range s.HttpRequest.Header {
		if s.View.shouldIndexHeader(key) {
			_, err := sql.Parse([]byte(val[0]))
			if err != nil {
				return err
			}
			s.headers[key] = val[0]
		}
	}

	return nil
}

func (s *Session) indexQueryParams() error {
	values := s.HttpRequest.URL.Query()
	s.queryParams = make(map[string]string)
	for k, val := range values {
		if s.View.shouldIndexQueryParam(k) {
			_, err := sql.Parse([]byte(val[0]))
			if err != nil {
				return err
			}
			s.queryParams[k] = val[0]
		}
	}
	return nil
}

func (s *Session) isAnyRequiredParamMissing() error {
	params := s.View.filterRequiredParams()
	var paramValue string

	for i := range params {
		switch params[i].In.Kind {
		case QueryKind:
			paramValue = s.QueryParam(params[i].In.Name)
		case PathKind:
			paramValue = s.PathVariable(params[i].In.Name)
		case HeaderKind:
			paramValue = s.Header(params[i].In.Name)
		case CookieKind:
			paramValue = s.Cookie(params[i].In.Name)
		case DataViewKind:
			//Parameter already contains View, if it wouldn't error would be thrown during View Initialization.
			continue
		}

		if paramValue == "" {
			return fmt.Errorf("parameter %v is required in %v but was not found, or was empty", params[i].In.Name, string(params[i].In.Kind))
		}
	}

	return nil
}

//QueryParam returns query parameter value
func (s *Session) QueryParam(name string) string {
	return s.queryParams[name]
}

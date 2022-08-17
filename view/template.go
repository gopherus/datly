package view

import (
	"context"
	"fmt"
	"github.com/viant/datly/logger"
	"github.com/viant/datly/shared"
	"github.com/viant/datly/view/keywords"
	"github.com/viant/datly/view/parameter"
	rdata "github.com/viant/toolbox/data"
	"github.com/viant/velty"
	"github.com/viant/velty/est"
	"github.com/viant/xunsafe"
	"reflect"
	"strings"
)

var boolType = reflect.TypeOf(true)

type (
	Template struct {
		Source         string       `json:",omitempty" yaml:"source,omitempty"`
		SourceURL      string       `json:",omitempty" yaml:"sourceURL,omitempty"`
		Schema         *Schema      `json:",omitempty" yaml:"schema,omitempty"`
		PresenceSchema *Schema      `json:",omitempty" yaml:"presenceSchema,omitempty"`
		Parameters     []*Parameter `json:",omitempty" yaml:"parameters,omitempty"`

		sqlEvaluator     *Evaluator
		accessors        *Accessors
		_fields          []reflect.StructField
		_fieldIndex      map[string]int
		_parametersIndex ParametersIndex
		initialized      bool
		isTemplate       bool
		wasEmpty         bool
		_view            *View
	}

	Evaluator struct {
		planner       *velty.Planner
		executor      *est.Execution
		stateProvider func() *est.State
	}

	CommonParams struct {
		ColumnsIn   string `velty:"COLUMN_IN"`
		WhereClause string `velty:"CRITERIA"`
		Pagination  string `velty:"PAGINATION"`
	}

	Param struct {
		Name  string
		Alias string
		Table string
	}
)

func (t *Template) Init(ctx context.Context, resource *Resource, view *View) error {
	if t.initialized {
		return nil
	}

	t.wasEmpty = t.Source == "" && t.SourceURL == ""
	t.initialized = true

	err := t.loadSourceFromURL(ctx, resource)
	if err != nil {
		return err
	}

	t._view = view
	t._fieldIndex = map[string]int{}

	if t.Source != "" {
		t.Source = "( " + t.Source + " )"
	} else {
		t.Source = view.Source()
	}

	t.isTemplate = t.Source != view.Name && t.Source != view.Table

	if err := t.initTypes(ctx, resource); err != nil {
		return err
	}

	if err := t.initPresenceType(resource); err != nil {
		return err
	}

	if err := t.initSqlEvaluator(); err != nil {
		return err
	}

	t.initAccessors()

	if err := t.updateParametersFields(); err != nil {
		return err
	}

	t._parametersIndex = ParametersSlice(t.Parameters).Index()

	return nil
}

func (t *Template) loadSourceFromURL(ctx context.Context, resource *Resource) error {
	if t.SourceURL == "" {
		return nil
	}
	var err error
	t.Source, err = resource.LoadText(ctx, t.SourceURL)
	return err
}

func (t *Template) initTypes(ctx context.Context, resource *Resource) error {
	if t.Schema == nil || (t.Schema.Name == "" && t.Schema.Type() == nil) {
		return t.createSchemaFromParams(ctx, resource)
	}

	return t.inheritParamTypesFromSchema(ctx, resource)
}

func (t *Template) createSchemaFromParams(ctx context.Context, resource *Resource) error {
	t.Schema = &Schema{}

	builder := parameter.NewBuilder("")

	for _, param := range t.Parameters {
		if err := t.inheritAndInitParam(ctx, resource, param); err != nil {
			return err
		}

		if err := builder.AddType(param.Name, param.ActualParamType()); err != nil {
			return err
		}
	}

	t.Schema.setType(builder.Build())

	return nil
}

func (t *Template) addField(name string, rType reflect.Type) error {
	_, ok := t._fieldIndex[name]
	if ok {
		return fmt.Errorf("_field with %v name already exists", name)
	}

	field, err := TemplateField(name, rType)
	if err != nil {
		return err
	}

	t._fieldIndex[name] = len(t._fields)
	t._fields = append(t._fields, field)

	return nil
}

func TemplateField(name string, rType reflect.Type) (reflect.StructField, error) {
	if len(name) == 0 {
		return reflect.StructField{}, fmt.Errorf("template _field name can't be empty")
	}

	pkgPath := ""
	if name[0] < 'A' || name[0] > 'Z' {
		pkgPath = "github.com/viant/datly/router"
	}

	field := reflect.StructField{Name: name, Type: rType, PkgPath: pkgPath}
	return field, nil
}

func (t *Template) inheritParamTypesFromSchema(ctx context.Context, resource *Resource) error {
	if t.Schema.Type() == nil {
		rType, err := resource._types.Lookup(t.Schema.Name)
		if err != nil {
			return err
		}
		t.Schema.setType(rType)
	}

	for _, param := range t.Parameters {
		if err := t.inheritAndInitParam(ctx, resource, param); err != nil {
			return err
		}

		if err := param.Init(ctx, t._view, resource, t.Schema.Type()); err != nil {
			return err
		}
	}

	return nil
}

func NewEvaluator(paramSchema, presenceSchema reflect.Type, template string) (*Evaluator, error) {
	evaluator := &Evaluator{}
	var err error

	evaluator.planner = velty.New(velty.BufferSize(len(template)))
	if err = evaluator.planner.DefineVariable(keywords.ParamsKey, paramSchema); err != nil {
		return nil, err
	}

	if err = evaluator.planner.DefineVariable(keywords.ParamsMetadataKey, presenceSchema); err != nil {
		return nil, err
	}

	if err = evaluator.planner.DefineVariable(keywords.ViewKey, reflect.TypeOf(&Param{})); err != nil {
		return nil, err
	}

	if err = evaluator.planner.DefineVariable(Criteria, reflect.TypeOf(&CriteriaSanitizer{})); err != nil {
		return nil, err
	}

	if err = evaluator.planner.DefineVariable(Logger, reflect.TypeOf(&logger.Printer{})); err != nil {
		return nil, err
	}

	evaluator.executor, evaluator.stateProvider, err = evaluator.planner.Compile([]byte(template))
	if err != nil {
		return nil, err
	}

	return evaluator, nil
}

func (t *Template) EvaluateSource(externalParams, presenceMap interface{}, parent *View) (string, *CriteriaSanitizer, *logger.Printer, error) {
	printer := &logger.Printer{}
	if t.wasEmpty {
		return t.Source, &CriteriaSanitizer{}, printer, nil
	}

	viewParam := &Param{}
	if parent != nil {
		viewParam = asViewParam(parent)
	} else {
		viewParam = asViewParam(t._view)
	}

	params := &CriteriaSanitizer{}
	SQL, err := t.sqlEvaluator.Evaluate(t.Schema.Type(), externalParams, presenceMap, viewParam, params, printer)
	return SQL, params, printer, err
}

func (e *Evaluator) Evaluate(schemaType reflect.Type, externalParams, presenceMap interface{}, viewParam *Param, params *CriteriaSanitizer, logger *logger.Printer) (string, error) {
	externalType := reflect.TypeOf(externalParams)
	if schemaType != externalType {
		return "", fmt.Errorf("inompactible types, wanted %v got %T", schemaType.String(), externalParams)
	}

	newState := e.stateProvider()
	if externalParams != nil {
		if err := newState.SetValue(keywords.ParamsKey, externalParams); err != nil {
			return "", err
		}
	}

	if presenceMap != nil {
		if err := newState.SetValue(keywords.ParamsMetadataKey, presenceMap); err != nil {
			return "", err
		}
	}

	if err := newState.SetValue(keywords.ViewKey, viewParam); err != nil {
		return "", err
	}

	if err := newState.SetValue(Criteria, params); err != nil {
		return "", err
	}

	if err := newState.SetValue(Logger, logger); err != nil {
		return "", err
	}

	if err := e.executor.Exec(newState); err != nil {
		return "", err
	}

	return newState.Buffer.String(), nil
}

func asViewParam(parent *View) *Param {
	viewParam := &Param{
		Name:  parent.Name,
		Alias: parent.Alias,
		Table: parent.Table,
	}

	return viewParam
}

func (t *Template) inheritAndInitParam(ctx context.Context, resource *Resource, param *Parameter) error {
	return param.Init(ctx, t._view, resource, nil)
}

func (t *Template) initSqlEvaluator() error {
	if t.wasEmpty {
		return nil
	}

	evaluator, err := NewEvaluator(t.Schema.Type(), t.PresenceSchema.Type(), t.Source)
	if err != nil {
		return err
	}

	t.sqlEvaluator = evaluator
	return nil
}

func (t *Template) initPresenceType(resource *Resource) error {
	if t.PresenceSchema == nil {
		return t.initPresenceSchemaFromParams()
	}

	if t.PresenceSchema.Type() != nil {
		return nil
	}

	rType, err := resource._types.Lookup(t.PresenceSchema.Name)
	if err != nil {
		return err
	}

	t.PresenceSchema.setType(rType)
	return nil
}

func (t *Template) initPresenceSchemaFromParams() error {
	builder := parameter.NewBuilder("")

	for _, param := range t.Parameters {
		if err := builder.AddType(param.PresenceName, boolType); err != nil {
			return err
		}
	}

	t.PresenceSchema = &Schema{}
	t.PresenceSchema.setType(builder.Build())

	return nil
}

func (t *Template) updateParametersFields() error {
	for _, param := range t.Parameters {
		if err := param.SetPresenceField(t.PresenceSchema.Type()); err != nil {
			return err
		}

		accessor, err := t.AccessorByName(param.Name)
		if err != nil {
			return err
		}

		param.SetAccessor(accessor)
	}

	return nil
}

func (t *Template) initAccessors() {
	if t.accessors == nil {
		t.accessors = NewAccessors()
	}

	t.accessors.Init(t.Schema.Type())
}

func NewAccessors() *Accessors {
	return &Accessors{index: map[string]int{}}
}

func (t *Template) AccessorByName(name string) (*Accessor, error) {
	return t.accessors.AccessorByName(name)
}

func fieldByTemplateName(structType reflect.Type, name string) (*xunsafe.Field, error) {
	structType = shared.Elem(structType)

	field, ok := structType.FieldByName(name)
	if !ok {
		for i := 0; i < structType.NumField(); i++ {
			field = structType.Field(i)
			veltyTag := velty.Parse(field.Tag.Get("velty"))
			for _, fieldName := range veltyTag.Names {
				if fieldName == name {
					return xunsafe.NewField(field), nil
				}
			}
		}

		return nil, fmt.Errorf("not found _field %v at type %v", name, structType.String())
	}

	return xunsafe.NewField(field), nil
}

func (t *Template) IsActualTemplate() bool {
	return t.isTemplate
}

func (t *Template) Expand(placeholders *[]interface{}, SQL string, selector *Selector, params CommonParams, batchData *BatchData, sanitized *CriteriaSanitizer) (string, error) {
	values, err := parameter.Parse(SQL)
	if err != nil {
		return "", err
	}

	replacement := &rdata.Map{}

	for _, value := range values {
		if value.Key == "?" {
			placeholder, err := sanitized.Next()
			if err != nil {
				return "", err
			}

			*placeholders = append(*placeholders, placeholder)
			continue
		}

		key, val, err := t.prepareExpanded(value, params, selector, batchData, placeholders, sanitized)
		if err != nil {
			return "", err
		}

		if key == "" {
			continue
		}

		replacement.SetValue(key, val)
	}

	return replacement.ExpandAsText(SQL), err
}

func (t *Template) prepareExpanded(value *parameter.Value, params CommonParams, selector *Selector, batchData *BatchData, placeholders *[]interface{}, sanitized *CriteriaSanitizer) (string, string, error) {
	key, val, err := t.replacementEntry(value.Key, params, selector, batchData, placeholders, sanitized)
	if err != nil {
		return "", "", err
	}

	return key, val, err

}

func (t *Template) replacementEntry(key string, params CommonParams, selector *Selector, batchData *BatchData, placeholders *[]interface{}, sanitized *CriteriaSanitizer) (string, string, error) {
	switch key {
	case keywords.Pagination[1:]:
		return key, params.Pagination, nil
	case keywords.Criteria[1:]:
		criteriaExpanded, err := t.Expand(placeholders, params.WhereClause, selector, params, batchData, sanitized)
		if err != nil {
			return "", "", err
		}

		return key, criteriaExpanded, nil
	case keywords.ColumnsIn[1:]:
		*placeholders = append(*placeholders, batchData.ValuesBatch...)
		return key, params.ColumnsIn, nil
	case keywords.SelectorCriteria[1:]:
		*placeholders = append(*placeholders, selector.Placeholders...)
		return key, selector.Criteria, nil
	default:
		if strings.HasPrefix(key, keywords.WherePrefix) {
			_, aValue, err := t.replacementEntry(key[len(keywords.WherePrefix):], params, selector, batchData, placeholders, sanitized)
			if err != nil {
				return "", "", err
			}

			return t.valueWithPrefix(key, aValue, " WHERE ", false)
		}

		if strings.HasPrefix(key, keywords.AndPrefix) {
			_, aValue, err := t.replacementEntry(key[len(keywords.AndPrefix):], params, selector, batchData, placeholders, sanitized)
			if err != nil {
				return "", "", err
			}

			return t.valueWithPrefix(key, aValue, " AND ", true)
		}

		if strings.HasPrefix(key, keywords.OrPrefix) {
			_, aValue, err := t.replacementEntry(key[len(keywords.OrPrefix):], params, selector, batchData, placeholders, sanitized)
			if err != nil {
				return "", "", err
			}

			return t.valueWithPrefix(key, aValue, " OR ", true)
		}

		accessor, err := t.AccessorByName(key)
		if err != nil {
			return "", "", err
		}

		values, err := accessor.Values(selector.Parameters.Values)
		if err != nil {
			return "", "", err
		}

		*placeholders = append(*placeholders, values...)
		switch len(values) {
		case 0:
			return "", "", nil
		case 1:
			return key, "?", nil
		case 2:
			return key, "?, ?", nil
		case 3:
			return key, "?, ?, ?", nil
		case 4:
			return key, "?, ?, ?, ?", nil
		default:
			sb := strings.Builder{}
			sb.WriteByte('?')
			for i := 1; i < len(values); i++ {
				sb.WriteString(", ?")
			}
			return key, sb.String(), nil
		}

	}
}

func (t *Template) valueWithPrefix(key string, aValue, prefix string, wrapWithParentheses bool) (string, string, error) {
	if aValue == "" {
		return key, "", nil
	}

	if wrapWithParentheses {
		return key, prefix + "(" + aValue + ")", nil
	}

	return key, prefix + aValue, nil
}

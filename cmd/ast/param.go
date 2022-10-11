package ast

import (
	"fmt"
	"github.com/viant/datly/cmd/option"
	"github.com/viant/datly/transform/sanitize"
	"github.com/viant/datly/view"
	"net/http"
	"strings"
)

func (d *paramTypeDetector) correctUntyped(iterator *sanitize.ParamMetaIterator, meta *option.ViewMeta, route *option.Route) error {
	for iterator.Has() {
		paramMeta := iterator.Next()
		aParam, ok := meta.ParamByName(paramMeta.Holder)
		if !ok {
			continue
		}

		if err := d.updateParamIfNeeded(route.Const, aParam, paramMeta); err != nil {
			return err
		}

		paramType, ok := route.Declare[aParam.Name]
		if ok {
			aParam.DataType = paramType
			aParam.Assumed = false
		}

		if aParam.Kind == string(view.QueryKind) {
			if route.Method != "" && route.Method != http.MethodGet {
				aParam.Kind = string(view.RequestBodyKind)
			}
		}
	}

	return nil
}

func (d *paramTypeDetector) updateParamIfNeeded(consts map[string]interface{}, param *option.Parameter, meta *sanitize.ParamMeta) error {
	if _, ok := consts[param.Name]; ok {
		param.Kind = string(view.LiteralKind)
	}

	if meta.MetaType == nil {
		return nil
	}

	for _, aHint := range meta.MetaType.Hint {
		newParam := &option.Parameter{}
		_, err := sanitize.UnmarshalHint(aHint, newParam)
		if err != nil {
			return err
		}

		inherit(param, newParam)
	}

	if len(meta.MetaType.SQL) > 1 {
		return fmt.Errorf("found multiple SQL statements for one parameter %v, SQL: %v", param.Name, meta.MetaType.SQL)
	}

	if len(meta.MetaType.SQL) == 1 {
		param.Kind = string(view.DataViewKind)
		param.SQL = meta.MetaType.SQL[0]
	}

	if len(meta.MetaType.Typer) > 0 {
		param.Typer = meta.MetaType.Typer[0]
	}

	if strings.EqualFold(meta.SQLKeyword, sanitize.InKeyword) {
		param.Repeated = true
	}

	return nil
}

func IsDataViewKind(hint string) bool {
	_, sqlPart := sanitize.SplitHint(hint)
	if strings.HasSuffix(sqlPart, "*/") {
		sqlPart = sqlPart[:len(sqlPart)-len("*/")]
	}

	return strings.TrimSpace(sqlPart) != ""
}

func inherit(generated *option.Parameter, inlined *option.Parameter) {
	if inlined.DataType != "" {
		generated.DataType = inlined.DataType
		generated.Assumed = false
	}

	if inlined.Required != nil {
		generated.Required = inlined.Required
	}

	if inlined.Name != "" {
		generated.Name = inlined.Name
	}

	if inlined.Kind != "" {
		generated.Kind = inlined.Kind
	}

	if inlined.Id != "" {
		generated.Id = inlined.Id
	}

	if inlined.Codec != "" {
		generated.Codec = inlined.Codec
	}
}

// Copyright 2026 Dolthub, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package aggregate

import (
	"strconv"
	"strings"

	"github.com/cockroachdb/apd/v3"
	"github.com/cockroachdb/errors"
	"github.com/dolthub/go-mysql-server/sql"
	gmstypes "github.com/dolthub/go-mysql-server/sql/types"

	"github.com/dolthub/doltgresql/postgres/parser/pgcode"
	"github.com/dolthub/doltgresql/postgres/parser/pgerror"
	"github.com/dolthub/doltgresql/server/functions"
	"github.com/dolthub/doltgresql/server/functions/framework"
	pgtypes "github.com/dolthub/doltgresql/server/types"
)

// initJsonAggs registers the JSON aggregate functions to the catalog.
func initJsonAggs() {
	framework.RegisterAggregateFunction(jsonAgg)
	framework.RegisterAggregateFunction(jsonbObjectAgg)
}

var jsonbObjectAgg = framework.Func2Aggregate{
	Function2: framework.Function2{
		Name: "jsonb_object_agg", Return: pgtypes.JsonB,
		Parameters: [2]*pgtypes.DoltgresType{pgtypes.Any, pgtypes.Any}, Strict: false,
		Callable: func(ctx *sql.Context, paramsAndReturn [3]*pgtypes.DoltgresType, key, value any) (any, error) {
			return nil, nil
		},
	},
	NewAggBuffer: newJsonbObjectAggBuffer, NewAggWindowFunc: newJsonbObjectAggWindowFunction,
}

// jsonAgg represents PostgreSQL's json_agg(anyelement) aggregate.
var jsonAgg = framework.Func1Aggregate{
	Function1: framework.Function1{
		Name:       "json_agg",
		Return:     pgtypes.Json,
		Parameters: [1]*pgtypes.DoltgresType{pgtypes.AnyElement},
		// json_agg is deliberately not strict: an input SQL NULL contributes a
		// JSON null element, while no input rows produce SQL NULL.
		Strict: false,
		Callable: func(ctx *sql.Context, paramsAndReturn [2]*pgtypes.DoltgresType, val any) (any, error) {
			return nil, nil
		},
	},
	NewAggBuffer:     newJsonAggBuffer,
	NewAggWindowFunc: newJsonAggWindowFunction,
}

// jsonAggBuffer accumulates the JSON representation of each input row.
type jsonAggBuffer struct {
	expr     sql.Expression
	elemType *pgtypes.DoltgresType
	elements []string
}

var _ sql.AggregationBuffer = (*jsonAggBuffer)(nil)

// newJsonAggBuffer creates an aggregation buffer for json_agg.
func newJsonAggBuffer(exprs []sql.Expression) (sql.AggregationBuffer, error) {
	return &jsonAggBuffer{expr: exprs[0]}, nil
}

// Dispose implements sql.AggregationBuffer.
func (b *jsonAggBuffer) Dispose(ctx *sql.Context) {}

// Eval implements sql.AggregationBuffer.
func (b *jsonAggBuffer) Eval(ctx *sql.Context) (interface{}, error) {
	if len(b.elements) == 0 {
		return nil, nil
	}
	return joinJsonAggregateElements(b.elemType, b.elements), nil
}

// Update implements sql.AggregationBuffer.
func (b *jsonAggBuffer) Update(ctx *sql.Context, row sql.Row) error {
	value, include, err := framework.EvalAggregateArgument(ctx, b.expr, row)
	if err != nil {
		return err
	}
	if !include {
		return nil
	}
	if b.elemType == nil {
		var ok bool
		b.elemType, ok = b.expr.Type(ctx).(*pgtypes.DoltgresType)
		if !ok {
			return errors.Errorf("json_agg: expected PostgreSQL argument type, got %T", b.expr.Type(ctx))
		}
	}
	raw, err := functions.ValueToJsonRaw(ctx, b.elemType, value)
	if err != nil {
		return err
	}
	b.elements = append(b.elements, string(raw))
	return nil
}

// jsonAggWindowFunction computes json_agg over a window frame.
type jsonAggWindowFunction struct {
	framework.WindowFramerState
	expr sql.Expression
}

var _ sql.WindowFunction = (*jsonAggWindowFunction)(nil)

// newJsonAggWindowFunction creates a window-function implementation of json_agg.
func newJsonAggWindowFunction(exprs []sql.Expression, window *sql.WindowDefinition) (sql.WindowFunction, error) {
	wf := &jsonAggWindowFunction{expr: exprs[0]}
	if err := wf.BindFramer(window); err != nil {
		return nil, err
	}
	return wf, nil
}

// Compute implements sql.WindowFunction.
func (w *jsonAggWindowFunction) Compute(ctx *sql.Context, interval sql.WindowInterval, buffer sql.WindowBuffer) (interface{}, error) {
	if interval.End <= interval.Start {
		return nil, nil
	}
	elements := make([]string, 0, interval.End-interval.Start)
	elemType, ok := w.expr.Type(ctx).(*pgtypes.DoltgresType)
	if !ok {
		return nil, errors.Errorf("json_agg: expected PostgreSQL argument type, got %T", w.expr.Type(ctx))
	}
	for i := interval.Start; i < interval.End; i++ {
		value, err := w.expr.Eval(ctx, buffer[i])
		if err != nil {
			return nil, err
		}
		raw, err := functions.ValueToJsonRaw(ctx, elemType, value)
		if err != nil {
			return nil, err
		}
		elements = append(elements, string(raw))
	}
	return joinJsonAggregateElements(elemType, elements), nil
}

// joinJsonAggregateElements formats collected JSON values using PostgreSQL's aggregate layout.
func joinJsonAggregateElements(elemType *pgtypes.DoltgresType, elements []string) string {
	separator := ", "
	if elemType != nil && (elemType.IsArrayType() || elemType.IsCompositeType() || elemType.ID.TypeName() == "record") {
		separator = ", \n "
	}
	return "[" + strings.Join(elements, separator) + "]"
}

type jsonbObjectAggBuffer struct {
	keyExpr, valueExpr sql.Expression
	values             map[string]any
}

var _ sql.AggregationBuffer = (*jsonbObjectAggBuffer)(nil)

func newJsonbObjectAggBuffer(exprs []sql.Expression) (sql.AggregationBuffer, error) {
	if len(exprs) != 2 {
		return nil, errors.Errorf("jsonb_object_agg expects two arguments")
	}
	return &jsonbObjectAggBuffer{keyExpr: exprs[0], valueExpr: exprs[1]}, nil
}

func (b *jsonbObjectAggBuffer) Dispose(*sql.Context) {}

func (b *jsonbObjectAggBuffer) Eval(*sql.Context) (any, error) {
	if b.values == nil {
		return nil, nil
	}
	return buildJsonbObjectAggregate(b.values), nil
}

func (b *jsonbObjectAggBuffer) Update(ctx *sql.Context, row sql.Row) error {
	key, include, err := framework.EvalAggregateArgument(ctx, b.keyExpr, row)
	if err != nil || !include {
		return err
	}
	value, _, err := framework.EvalAggregateArgument(ctx, b.valueExpr, row)
	if err != nil {
		return err
	}
	keyText, err := jsonbObjectAggKey(ctx, b.keyExpr, key)
	if err != nil {
		return err
	}
	jsonValue, err := jsonbObjectAggValue(ctx, b.valueExpr, value)
	if err != nil {
		return err
	}
	if b.values == nil {
		b.values = make(map[string]any)
	}
	b.values[keyText] = jsonValue
	return nil
}

func jsonbObjectAggKey(ctx *sql.Context, expr sql.Expression, value any) (string, error) {
	if value == nil {
		return "", pgerror.WithCandidateCode(errors.New("field name must not be null"), pgcode.InvalidParameterValue)
	}
	typ, ok := expr.Type(ctx).(*pgtypes.DoltgresType)
	if !ok {
		return "", errors.Errorf("jsonb_object_agg: expected PostgreSQL key type, got %T", expr.Type(ctx))
	}
	if typ.TypType == pgtypes.TypeType_Domain {
		typ = typ.DomainUnderlyingBaseType()
	}
	if typ.IsArrayType() || typ.IsCompositeType() || typ.ID == pgtypes.Json.ID || typ.ID == pgtypes.JsonB.ID {
		return "", pgerror.WithCandidateCode(
			errors.New("key value must be scalar, not array, composite, or json"), pgcode.InvalidParameterValue)
	}
	value, err := sql.UnwrapAny(ctx, value)
	if err != nil {
		return "", err
	}
	if typ.ID == pgtypes.Bool.ID {
		if boolean, ok := value.(bool); ok {
			if boolean {
				return "true", nil
			}
			return "false", nil
		}
	}
	return typ.IoOutput(ctx, value)
}

func jsonbObjectAggValue(ctx *sql.Context, expr sql.Expression, value any) (any, error) {
	typ, ok := expr.Type(ctx).(*pgtypes.DoltgresType)
	if !ok {
		return nil, errors.Errorf("jsonb_object_agg: expected PostgreSQL value type, got %T", expr.Type(ctx))
	}
	raw, err := functions.ValueToJsonRaw(ctx, typ, value)
	if err != nil {
		return nil, err
	}
	document, err := pgtypes.UnmarshalToJsonDocument(raw)
	if err != nil {
		return nil, err
	}
	return jsonbAggregateValueToInterface(document.Value)
}

func jsonbAggregateValueToInterface(value pgtypes.JsonValue) (any, error) {
	switch value := value.(type) {
	case pgtypes.JsonValueObject:
		result := make(map[string]any, len(value.Items))
		for _, item := range value.Items {
			converted, err := jsonbAggregateValueToInterface(item.Value)
			if err != nil {
				return nil, err
			}
			result[item.Key] = converted
		}
		return result, nil
	case pgtypes.JsonValueArray:
		result := make([]any, len(value))
		for idx, item := range value {
			converted, err := jsonbAggregateValueToInterface(item)
			if err != nil {
				return nil, err
			}
			result[idx] = converted
		}
		return result, nil
	case pgtypes.JsonValueString:
		return string(value), nil
	case pgtypes.JsonValueNumber:
		decimal := apd.Decimal(value)
		text := decimal.Text('f')
		if integer, err := strconv.ParseInt(text, 10, 64); err == nil {
			if integer > -(1<<53) && integer < 1<<53 {
				return float64(integer), nil
			}
			return integer, nil
		}
		if unsigned, err := strconv.ParseUint(text, 10, 64); err == nil {
			if unsigned < 1<<53 {
				return float64(unsigned), nil
			}
			return unsigned, nil
		}
		floating, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return nil, errors.Errorf("jsonb_object_agg cannot preserve numeric value %s", text)
		}
		roundTrip, _, err := apd.NewFromString(strconv.FormatFloat(floating, 'g', -1, 64))
		if err != nil || decimal.Cmp(roundTrip) != 0 {
			return nil, errors.Errorf("jsonb_object_agg cannot preserve numeric value %s", text)
		}
		return floating, nil
	case pgtypes.JsonValueBoolean:
		return bool(value), nil
	case pgtypes.JsonValueNull:
		return nil, nil
	default:
		return nil, errors.Errorf("jsonb_object_agg: unsupported JSON value %T", value)
	}
}

func buildJsonbObjectAggregate(values map[string]any) sql.JSONWrapper {
	return gmstypes.JSONDocument{Val: values}
}

type jsonbObjectAggWindowFunction struct {
	framework.WindowFramerState
	keyExpr, valueExpr sql.Expression
}

var _ sql.WindowFunction = (*jsonbObjectAggWindowFunction)(nil)

func newJsonbObjectAggWindowFunction(exprs []sql.Expression, window *sql.WindowDefinition) (sql.WindowFunction, error) {
	if len(exprs) != 2 {
		return nil, errors.Errorf("jsonb_object_agg expects two arguments")
	}
	w := &jsonbObjectAggWindowFunction{keyExpr: exprs[0], valueExpr: exprs[1]}
	if err := w.BindFramer(window); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *jsonbObjectAggWindowFunction) Compute(ctx *sql.Context, interval sql.WindowInterval, buffer sql.WindowBuffer) (any, error) {
	if interval.End <= interval.Start {
		return nil, nil
	}
	values := make(map[string]any)
	for idx := interval.Start; idx < interval.End; idx++ {
		key, err := w.keyExpr.Eval(ctx, buffer[idx])
		if err != nil {
			return nil, err
		}
		value, err := w.valueExpr.Eval(ctx, buffer[idx])
		if err != nil {
			return nil, err
		}
		keyText, err := jsonbObjectAggKey(ctx, w.keyExpr, key)
		if err != nil {
			return nil, err
		}
		jsonValue, err := jsonbObjectAggValue(ctx, w.valueExpr, value)
		if err != nil {
			return nil, err
		}
		values[keyText] = jsonValue
	}
	return buildJsonbObjectAggregate(values), nil
}

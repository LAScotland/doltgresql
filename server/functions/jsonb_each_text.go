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

package functions

import (
	"io"

	"github.com/cockroachdb/errors"
	"github.com/dolthub/go-mysql-server/sql"
	"github.com/dolthub/go-mysql-server/sql/types"

	"github.com/dolthub/doltgresql/postgres/parser/pgcode"
	"github.com/dolthub/doltgresql/postgres/parser/pgerror"
	"github.com/dolthub/doltgresql/server/functions/framework"
	pgtypes "github.com/dolthub/doltgresql/server/types"
)

const jsonbEachTextName = "jsonb_each_text"

// jsonb_each_text_jsonb expands the top-level fields of a JSONB object. Text
// values are returned without JSON quotes, and a JSON null becomes SQL NULL.
var jsonb_each_text_jsonb = framework.Function1{
	Name:       jsonbEachTextName,
	Return:     pgtypes.Record,
	Parameters: [1]*pgtypes.DoltgresType{pgtypes.JsonB},
	Strict:     true,
	SRF:        true,
	Callable: func(ctx *sql.Context, _ [2]*pgtypes.DoltgresType, val any) (any, error) {
		v, err := jsonValueToInterface(ctx, val)
		if err != nil {
			return nil, err
		}
		obj, ok := v.(map[string]any)
		if !ok {
			return nil, pgerror.WithCandidateCode(
				errors.Errorf("cannot call %s on a non-object", jsonbEachTextName),
				pgcode.InvalidParameterValue,
			)
		}

		keys := sortedJsonObjectKeys(obj)
		idx := 0
		return pgtypes.NewSetReturningFunctionRowIter(func(ctx *sql.Context) (sql.Row, error) {
			if idx >= len(keys) {
				return nil, io.EOF
			}
			key := keys[idx]
			idx++
			value := obj[key]
			if value == nil {
				return sql.Row{key, nil}, nil
			}
			if text, ok := value.(string); ok {
				return sql.Row{key, text}, nil
			}
			text, err := jsonWrapperToFormattedString(ctx, types.JSONDocument{Val: value})
			if err != nil {
				return nil, err
			}
			return sql.Row{key, text}, nil
		}), nil
	},
	OutParams: sql.Schema{
		{Name: "key", Type: pgtypes.Text, Nullable: false, Source: jsonbEachTextName},
		{Name: "value", Type: pgtypes.Text, Nullable: true, Source: jsonbEachTextName},
	},
}

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
	"github.com/cockroachdb/errors"
	"github.com/dolthub/go-mysql-server/sql"
	gmstypes "github.com/dolthub/go-mysql-server/sql/types"

	"github.com/dolthub/doltgresql/postgres/parser/pgcode"
	"github.com/dolthub/doltgresql/postgres/parser/pgerror"
	"github.com/dolthub/doltgresql/server/functions/framework"
	pgtypes "github.com/dolthub/doltgresql/server/types"
)

// jsonb_path_query_array_jsonb_text is a bounded compatibility overload for
// clients that use the root and object-member wildcard paths.
var jsonb_path_query_array_jsonb_text = framework.Function2{
	Name:       "jsonb_path_query_array",
	Return:     pgtypes.JsonB,
	Parameters: [2]*pgtypes.DoltgresType{pgtypes.JsonB, pgtypes.Text},
	Strict:     true,
	Callable: func(ctx *sql.Context, _ [3]*pgtypes.DoltgresType, target, pathVal any) (any, error) {
		path, err := framework.UnwrapString(ctx, pathVal)
		if err != nil {
			return nil, err
		}
		mode, path, err := parseSupportedJsonPathFor("jsonb_path_query_array", path)
		if err != nil {
			return nil, err
		}
		value, err := jsonValueToInterface(ctx, target)
		if err != nil {
			return nil, err
		}
		switch path {
		case "$":
			return gmstypes.JSONDocument{Val: []any{value}}, nil
		case "$.*":
			values, err := allJsonObjectMembers(value, mode)
			if err != nil {
				return nil, err
			}
			return gmstypes.JSONDocument{Val: values}, nil
		default:
			return nil, pgerror.WithCandidateCode(
				errors.Errorf("jsonb_path_query_array compatibility overload does not support path %q", path),
				pgcode.FeatureNotSupported,
			)
		}
	},
}

func allJsonObjectMembers(value any, mode jsonPathMode) ([]any, error) {
	if object, ok := value.(map[string]any); ok {
		return appendJsonObjectMembers(nil, object), nil
	}
	if mode == jsonPathLax {
		var matches []any
		if items, ok := value.([]any); ok {
			for _, item := range items {
				if object, ok := item.(map[string]any); ok {
					matches = appendJsonObjectMembers(matches, object)
				}
			}
		}
		if matches == nil {
			matches = []any{}
		}
		return matches, nil
	}
	return nil, pgerror.WithCandidateCode(
		errors.New("jsonpath wildcard member accessor can only be applied to an object"),
		pgcode.MakeCode("2203C"),
	)
}

func appendJsonObjectMembers(values []any, object map[string]any) []any {
	for _, key := range sortedJsonObjectKeys(object) {
		values = append(values, object[key])
	}
	return values
}

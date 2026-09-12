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
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/dolthub/go-mysql-server/sql"
	gmstypes "github.com/dolthub/go-mysql-server/sql/types"

	"github.com/dolthub/doltgresql/postgres/parser/pgcode"
	"github.com/dolthub/doltgresql/postgres/parser/pgerror"
	"github.com/dolthub/doltgresql/server/functions/framework"
	pgtypes "github.com/dolthub/doltgresql/server/types"
)

// jsonb_path_query_first_jsonb_text is a deliberately bounded compatibility
// overload. Doltgres does not yet implement PostgreSQL's jsonpath type or its
// general evaluator; this overload supports only the root and object wildcard
// paths needed by current clients.
var jsonb_path_query_first_jsonb_text = framework.Function2{
	Name:       "jsonb_path_query_first",
	Return:     pgtypes.JsonB,
	Parameters: [2]*pgtypes.DoltgresType{pgtypes.JsonB, pgtypes.Text},
	Strict:     true,
	Callable: func(ctx *sql.Context, _ [3]*pgtypes.DoltgresType, target, pathVal any) (any, error) {
		path, err := framework.UnwrapString(ctx, pathVal)
		if err != nil {
			return nil, err
		}
		mode, path, err := parseSupportedJsonPath(path)
		if err != nil {
			return nil, err
		}
		value, err := jsonValueToInterface(ctx, target)
		if err != nil {
			return nil, err
		}

		switch path {
		case "$":
			return gmstypes.JSONDocument{Val: value}, nil
		case "$.*":
			return firstJsonObjectMember(value, mode)
		default:
			return nil, pgerror.WithCandidateCode(
				errors.Errorf("jsonb_path_query_first compatibility overload does not support path %q", path),
				pgcode.FeatureNotSupported,
			)
		}
	},
}

type jsonPathMode byte

const (
	jsonPathLax jsonPathMode = iota
	jsonPathStrict
)

func parseSupportedJsonPath(input string) (jsonPathMode, string, error) {
	return parseSupportedJsonPathFor("jsonb_path_query_first", input)
}

func parseSupportedJsonPathFor(functionName, input string) (jsonPathMode, string, error) {
	path := strings.TrimSpace(input)
	mode := jsonPathLax
	if rest, ok := strings.CutPrefix(path, "lax "); ok {
		path = strings.TrimSpace(rest)
	} else if rest, ok := strings.CutPrefix(path, "strict "); ok {
		mode = jsonPathStrict
		path = strings.TrimSpace(rest)
	}
	if path != "$" && path != "$.*" {
		return mode, "", pgerror.WithCandidateCode(
			errors.Errorf("%s compatibility overload does not support path %q", functionName, input),
			pgcode.FeatureNotSupported,
		)
	}
	return mode, path, nil
}

func firstJsonObjectMember(value any, mode jsonPathMode) (any, error) {
	if obj, ok := value.(map[string]any); ok {
		keys := sortedJsonObjectKeys(obj)
		if len(keys) == 0 {
			return nil, nil
		}
		return gmstypes.JSONDocument{Val: obj[keys[0]]}, nil
	}

	if mode == jsonPathLax {
		if values, ok := value.([]any); ok {
			for _, item := range values {
				obj, ok := item.(map[string]any)
				if !ok {
					continue
				}
				keys := sortedJsonObjectKeys(obj)
				if len(keys) > 0 {
					return gmstypes.JSONDocument{Val: obj[keys[0]]}, nil
				}
			}
		}
		return nil, nil
	}

	return nil, pgerror.WithCandidateCode(
		errors.New("jsonpath wildcard member accessor can only be applied to an object"),
		pgcode.MakeCode("2203C"),
	)
}

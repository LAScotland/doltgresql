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

package binary

import (
	"encoding/json"
	"strconv"
	"strings"
	"unicode"

	"github.com/cockroachdb/apd/v3"
	"github.com/cockroachdb/errors"
	"github.com/dolthub/go-mysql-server/sql"

	"github.com/dolthub/doltgresql/postgres/parser/pgcode"
	"github.com/dolthub/doltgresql/postgres/parser/pgerror"
	"github.com/dolthub/doltgresql/server/functions/framework"
	pgtypes "github.com/dolthub/doltgresql/server/types"
)

// jsonb_path_exists_opr is the deliberately bounded implementation of @?. It
// supports the object-member numeric predicates emitted by Odoo for company
// dependent many2one fields.
var jsonb_path_exists_opr = framework.Function2{
	Name:       "jsonb_path_exists_opr",
	Return:     pgtypes.Bool,
	Parameters: [2]*pgtypes.DoltgresType{pgtypes.JsonB, pgtypes.Text},
	Strict:     true,
	Callable: func(ctx *sql.Context, _ [3]*pgtypes.DoltgresType, target, pathVal any) (any, error) {
		path, err := framework.UnwrapString(ctx, pathVal)
		if err != nil {
			return nil, err
		}
		mode, wanted, err := parseNumericObjectMemberPredicate(path)
		if err != nil {
			return nil, err
		}
		wrapper, err := toJSONWrapper(ctx, target)
		if err != nil {
			return nil, err
		}
		value, err := wrapper.ToInterface(ctx)
		if err != nil {
			return nil, err
		}
		members, err := jsonPathObjectMembers(value, mode)
		if err != nil {
			// PostgreSQL's @? operator suppresses structural JSONPath errors.
			// A strict member wildcard on a non-object therefore yields UNKNOWN.
			if mode == pathModeStrict {
				return nil, nil
			}
			return nil, err
		}
		for _, member := range members {
			unwrapDepth := 0
			if mode == pathModeLax {
				// Lax filter application unwraps its item sequence once, and lax
				// comparison unwraps an array operand once more.
				unwrapDepth = 2
			}
			if jsonPathNumericMatch(member, wanted, unwrapDepth) {
				return true, nil
			}
		}
		return false, nil
	},
}

type pathMode byte

const (
	pathModeLax pathMode = iota
	pathModeStrict
)

func unsupportedPath(path string) error {
	return pgerror.WithCandidateCode(
		errors.Errorf("jsonb @? compatibility operator does not support path %q", path),
		pgcode.FeatureNotSupported,
	)
}

// parseNumericObjectMemberPredicate accepts:
//
//	[lax|strict] $.* ? (@ == number || @ == number ...)
func parseNumericObjectMemberPredicate(input string) (pathMode, []*apd.Decimal, error) {
	p := &numericPredicateParser{input: input}
	mode := pathModeLax
	p.space()
	if p.word("lax") {
		p.space()
	} else if p.word("strict") {
		mode = pathModeStrict
		p.space()
	}
	if !p.take("$.*") {
		return mode, nil, unsupportedPath(input)
	}
	p.space()
	if !p.take("?") {
		return mode, nil, unsupportedPath(input)
	}
	p.space()
	if !p.take("(") {
		return mode, nil, unsupportedPath(input)
	}

	var values []*apd.Decimal
	for {
		p.space()
		if !p.take("@") {
			return mode, nil, unsupportedPath(input)
		}
		p.space()
		if !p.take("==") {
			return mode, nil, unsupportedPath(input)
		}
		p.space()
		start := p.pos
		if p.pos < len(p.input) && (p.input[p.pos] == '+' || p.input[p.pos] == '-') {
			p.pos++
		}
		digits := false
		for p.pos < len(p.input) && p.input[p.pos] >= '0' && p.input[p.pos] <= '9' {
			digits = true
			p.pos++
		}
		if p.pos < len(p.input) && p.input[p.pos] == '.' {
			p.pos++
			for p.pos < len(p.input) && p.input[p.pos] >= '0' && p.input[p.pos] <= '9' {
				digits = true
				p.pos++
			}
		}
		if !digits {
			return mode, nil, unsupportedPath(input)
		}
		if p.pos < len(p.input) && (p.input[p.pos] == 'e' || p.input[p.pos] == 'E') {
			p.pos++
			if p.pos < len(p.input) && (p.input[p.pos] == '+' || p.input[p.pos] == '-') {
				p.pos++
			}
			exponentStart := p.pos
			for p.pos < len(p.input) && p.input[p.pos] >= '0' && p.input[p.pos] <= '9' {
				p.pos++
			}
			if exponentStart == p.pos {
				return mode, nil, unsupportedPath(input)
			}
		}
		value, _, err := apd.NewFromString(p.input[start:p.pos])
		if err != nil || value.Form != apd.Finite {
			return mode, nil, unsupportedPath(input)
		}
		values = append(values, value)
		p.space()
		if p.take(")") {
			p.space()
			if p.pos != len(p.input) {
				return mode, nil, unsupportedPath(input)
			}
			return mode, values, nil
		}
		if !p.take("||") {
			return mode, nil, unsupportedPath(input)
		}
	}
}

type numericPredicateParser struct {
	input string
	pos   int
}

func (p *numericPredicateParser) space() {
	for p.pos < len(p.input) && unicode.IsSpace(rune(p.input[p.pos])) {
		p.pos++
	}
}

func (p *numericPredicateParser) take(s string) bool {
	if !strings.HasPrefix(p.input[p.pos:], s) {
		return false
	}
	p.pos += len(s)
	return true
}

func (p *numericPredicateParser) word(s string) bool {
	if !strings.HasPrefix(p.input[p.pos:], s) {
		return false
	}
	end := p.pos + len(s)
	if end < len(p.input) && !unicode.IsSpace(rune(p.input[end])) {
		return false
	}
	p.pos = end
	return true
}

func jsonPathObjectMembers(value any, mode pathMode) ([]any, error) {
	if object, ok := value.(map[string]any); ok {
		members := make([]any, 0, len(object))
		for _, member := range object {
			members = append(members, member)
		}
		return members, nil
	}
	if mode == pathModeLax {
		var members []any
		if items, ok := value.([]any); ok {
			for _, item := range items {
				if object, ok := item.(map[string]any); ok {
					for _, member := range object {
						members = append(members, member)
					}
				}
			}
		}
		return members, nil
	}
	return nil, pgerror.WithCandidateCode(
		errors.New("jsonpath wildcard member accessor can only be applied to an object"),
		pgcode.MakeCode("2203C"),
	)
}

func jsonPathNumericMatch(value any, wanted []*apd.Decimal, unwrapDepth int) bool {
	if unwrapDepth > 0 {
		if values, ok := value.([]any); ok {
			for _, item := range values {
				if jsonPathNumericMatch(item, wanted, unwrapDepth-1) {
					return true
				}
			}
			return false
		}
	}
	valueDecimal, ok := jsonNumberDecimal(value)
	if !ok {
		return false
	}
	for _, candidate := range wanted {
		if valueDecimal.Cmp(candidate) == 0 {
			return true
		}
	}
	return false
}

func jsonNumberDecimal(value any) (*apd.Decimal, bool) {
	switch v := value.(type) {
	case *apd.Decimal:
		return v, true
	case apd.Decimal:
		return &v, true
	case pgtypes.JsonValueNumber:
		d := apd.Decimal(v)
		return &d, true
	case json.Number:
		d, _, err := apd.NewFromString(string(v))
		return d, err == nil
	case float64:
		// GMS JSONDocument currently exposes parsed JSON numbers as float64.
		// FormatFloat's shortest round-trip form avoids adding binary floating
		// point noise before the exact decimal comparison below.
		d, _, err := apd.NewFromString(strconv.FormatFloat(v, 'g', -1, 64))
		return d, err == nil
	case int64:
		d, _, err := apd.NewFromString(strconv.FormatInt(v, 10))
		return d, err == nil
	case uint64:
		d, _, err := apd.NewFromString(strconv.FormatUint(v, 10))
		return d, err == nil
	default:
		return nil, false
	}
}

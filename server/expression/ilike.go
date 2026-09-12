// Copyright 2026 Dolthub, Inc.
// Licensed under the Apache License, Version 2.0 (the "License");

package expression

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/cockroachdb/errors"
	"github.com/dolthub/go-mysql-server/sql"
	gmsexpression "github.com/dolthub/go-mysql-server/sql/expression"
	gmstypes "github.com/dolthub/go-mysql-server/sql/types"
	vitess "github.com/dolthub/vitess/go/vt/sqlparser"

	pgtypes "github.com/dolthub/doltgresql/server/types"
)

// ILike implements PostgreSQL's case-insensitive LIKE operators. It delegates
// wildcard and escape processing to GMS's LIKE matcher after Unicode case
// folding both operands. strings.ToLower is locale independent, so this does
// not attempt to reproduce locale-specific PostgreSQL collations.
type ILike struct {
	children []sql.Expression
	negated  bool
}

var _ sql.Expression = (*ILike)(nil)
var _ vitess.Injectable = (*ILike)(nil)

func NewILike(negated bool) *ILike { return &ILike{negated: negated} }

func (i *ILike) Children() []sql.Expression { return i.children }
func (i *ILike) Resolved() bool {
	if len(i.children) < 2 || len(i.children) > 3 {
		return false
	}
	for _, child := range i.children {
		if !child.Resolved() {
			return false
		}
	}
	return true
}
func (i *ILike) IsNullable(*sql.Context) bool { return true }
func (i *ILike) Type(*sql.Context) sql.Type   { return pgtypes.Bool }

func (i *ILike) Eval(ctx *sql.Context, row sql.Row) (any, error) {
	if len(i.children) < 2 || len(i.children) > 3 {
		return nil, errors.Errorf("ILIKE requires two operands and an optional escape")
	}
	left, err := evalILikeText(ctx, row, i.children[0])
	if err != nil || left == nil {
		return nil, err
	}
	right, err := evalILikeText(ctx, row, i.children[1])
	if err != nil || right == nil {
		return nil, err
	}
	escape := '\\'
	if len(i.children) == 3 {
		escapeText, err := evalILikeText(ctx, row, i.children[2])
		if err != nil || escapeText == nil {
			return nil, err
		}
		switch utf8.RuneCountInString(*escapeText) {
		case 0:
			escape = 0
		case 1:
			escape, _ = utf8.DecodeRuneInString(*escapeText)
		default:
			return nil, sql.ErrInvalidArgument.New("ESCAPE")
		}
	}
	lc, lco := sql.GetCoercibility(ctx, i.children[0])
	rc, rco := sql.GetCoercibility(ctx, i.children[1])
	collation, _ := sql.ResolveCoercibility(lc, lco, rc, rco)
	foldedPattern, matcherEscape := foldILikePattern(*right, escape)
	matcher, err := gmsexpression.ConstructLikeMatcher(collation, foldedPattern, matcherEscape)
	if err != nil {
		return nil, err
	}
	matched := matcher.Match(strings.ToLower(*left))
	if i.negated {
		matched = !matched
	}
	return matched, nil
}

func (i *ILike) String() string {
	op := "ILIKE"
	if i.negated {
		op = "NOT ILIKE"
	}
	if len(i.children) < 2 {
		return op
	}
	s := fmt.Sprintf("%s %s %s", i.children[0], op, i.children[1])
	if len(i.children) == 3 {
		s += fmt.Sprintf(" ESCAPE %s", i.children[2])
	}
	return s
}

func (i *ILike) WithChildren(_ *sql.Context, children ...sql.Expression) (sql.Expression, error) {
	if len(children) < 2 || len(children) > 3 {
		return nil, sql.ErrInvalidChildrenNumber.New(i, len(children), 2)
	}
	return &ILike{children: children, negated: i.negated}, nil
}

func (i *ILike) WithResolvedChildren(_ context.Context, children []any) (any, error) {
	exprs := make([]sql.Expression, len(children))
	for idx, child := range children {
		var ok bool
		exprs[idx], ok = child.(sql.Expression)
		if !ok {
			return nil, errors.Errorf("expected ILIKE child to be an expression, found %T", child)
		}
	}
	return i.WithChildren(nil, exprs...)
}

func evalILikeText(ctx *sql.Context, row sql.Row, child sql.Expression) (*string, error) {
	value, err := child.Eval(ctx, row)
	if err != nil || value == nil {
		return nil, err
	}
	value, err = sql.UnwrapAny(ctx, value)
	if err != nil {
		return nil, err
	}
	var text string
	if stringValue, ok := value.(string); ok {
		text = stringValue
	} else {
		text, _, err = gmstypes.ConvertToCollatedString(ctx, value, child.Type(ctx))
		if err != nil {
			return nil, err
		}
	}
	return &text, nil
}

// foldILikePattern preserves which characters are escape markers before case
// folding. This matters when a literal differs from the escape character only
// by case, for example: 'A' ILIKE 'A' ESCAPE 'a'.
func foldILikePattern(pattern string, escape rune) (string, rune) {
	if escape == 0 {
		return strings.ToLower(pattern), 0
	}

	runes := []rune(pattern)
	var builder strings.Builder
	for idx := 0; idx < len(runes); idx++ {
		if runes[idx] == escape {
			builder.WriteRune('\\')
			if idx+1 < len(runes) {
				idx++
				builder.WriteString(strings.ToLower(string(runes[idx])))
			}
			continue
		}
		if runes[idx] == '\\' {
			builder.WriteRune('\\')
		}
		builder.WriteString(strings.ToLower(string(runes[idx])))
	}
	return builder.String(), '\\'
}

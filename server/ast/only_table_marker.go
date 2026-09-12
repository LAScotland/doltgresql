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

package ast

import (
	"context"
	"github.com/dolthub/doltgresql/postgres/parser/sem/tree"

	gmsexpression "github.com/dolthub/go-mysql-server/sql/expression"
	gmstypes "github.com/dolthub/go-mysql-server/sql/types"
)

// OnlyTableMarker is a typed planner carrier for PostgreSQL's ONLY relation
// modifier. The AST conversion places it in a derived table filter so GMS will
// resolve the underlying scan normally. A Doltgres analyzer rule must consume
// the marker and filter before optimization or execution.
type OnlyTableMarker struct{}

// WithResolvedChildren implements vitess.Injectable.
func (*OnlyTableMarker) WithResolvedChildren(context.Context, []any) (any, error) {
	return &OnlyTableMarkerExpression{Literal: gmsexpression.NewLiteral(true, gmstypes.Boolean)}, nil
}

// OnlyTableMarkerExpression has a concrete Go type so SQL text cannot forge
// ONLY metadata. Literal supplies the leaf sql.Expression implementation.
type OnlyTableMarkerExpression struct {
	*gmsexpression.Literal
}

func explicitOnlyTable(expr tree.TableExpr) bool {
	switch t := expr.(type) {
	case *tree.AliasedTableExpr:
		return explicitOnlyTable(t.Expr)
	case *tree.TableName:
		return t.ExplicitOnly
	case *tree.ParenTableExpr:
		return explicitOnlyTable(t.Expr)
	}
	return false
}

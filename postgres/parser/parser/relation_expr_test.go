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

package parser

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dolthub/doltgresql/postgres/parser/sem/tree"
)

func TestRelationExprPreservesExplicitOnly(t *testing.T) {
	tests := []struct {
		query     string
		tableName func(tree.Statement) bool
	}{
		{
			query: "select * from only parent_table",
			tableName: func(stmt tree.Statement) bool {
				from := stmt.(*tree.Select).Select.(*tree.SelectClause).From.Tables[0].(*tree.AliasedTableExpr)
				return from.Expr.(*tree.TableName).ExplicitOnly
			},
		},
		{
			query: "select * from only (parent_table)",
			tableName: func(stmt tree.Statement) bool {
				from := stmt.(*tree.Select).Select.(*tree.SelectClause).From.Tables[0].(*tree.AliasedTableExpr)
				return from.Expr.(*tree.TableName).ExplicitOnly
			},
		},
		{
			query: "select * from only parent_table *",
			tableName: func(stmt tree.Statement) bool {
				from := stmt.(*tree.Select).Select.(*tree.SelectClause).From.Tables[0].(*tree.AliasedTableExpr)
				return from.Expr.(*tree.TableName).ExplicitOnly
			},
		},
		{
			query: "update only parent_table set value = 1",
			tableName: func(stmt tree.Statement) bool {
				return stmt.(*tree.Update).Table.(*tree.AliasedTableExpr).Expr.(*tree.TableName).ExplicitOnly
			},
		},
		{
			query: "delete from only parent_table",
			tableName: func(stmt tree.Statement) bool {
				return stmt.(*tree.Delete).Table.(*tree.AliasedTableExpr).Expr.(*tree.TableName).ExplicitOnly
			},
		},
		{
			query: "truncate only parent_table",
			tableName: func(stmt tree.Statement) bool {
				return stmt.(*tree.Truncate).Tables[0].ExplicitOnly
			},
		},
		{
			query: "alter table only parent_table add column value int",
			tableName: func(stmt tree.Statement) bool {
				return stmt.(*tree.AlterTable).Table.ExplicitOnly
			},
		},
	}

	for _, test := range tests {
		t.Run(test.query, func(t *testing.T) {
			parsed, err := ParseOne(test.query)
			require.NoError(t, err)
			require.True(t, test.tableName(parsed.AST))
			require.Contains(t, tree.AsString(parsed.AST), "ONLY parent_table")
		})
	}
}

func TestRelationExprLeavesOrdinaryTableUnmarked(t *testing.T) {
	for _, query := range []string{"select * from parent_table", "select * from parent_table *"} {
		parsed, err := ParseOne(query)
		require.NoError(t, err)
		from := parsed.AST.(*tree.Select).Select.(*tree.SelectClause).From.Tables[0].(*tree.AliasedTableExpr)
		require.False(t, from.Expr.(*tree.TableName).ExplicitOnly)
	}
}

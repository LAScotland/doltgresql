// Copyright 2023 Dolthub, Inc.
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
	"github.com/cockroachdb/errors"
	vitess "github.com/dolthub/vitess/go/vt/sqlparser"

	"github.com/dolthub/doltgresql/postgres/parser/sem/tree"
)

// nodeUpdateExpr handles *tree.UpdateExpr nodes.
func nodeUpdateExpr(ctx *Context, node *tree.UpdateExpr) (vitess.AssignmentExprs, error) {
	if node == nil {
		return nil, nil
	}
	if node.Tuple {
		return nodeTupleUpdateExpr(ctx, node)
	}
	expr, err := nodeExpr(ctx, node.Expr)
	if err != nil {
		return nil, err
	}
	var assignmentExprs []*vitess.AssignmentExpr
	for _, name := range node.Names {
		assignmentExprs = append(assignmentExprs, &vitess.AssignmentExpr{
			Name: &vitess.ColName{
				Name: vitess.NewColIdent(string(name)),
			},
			Expr: expr,
		})
	}
	return assignmentExprs, nil
}

// nodeTupleUpdateExpr assigns each target column the expression at the same position in the RHS tuple. Keeping the
// expressions as sibling assignments lets the update planner evaluate all of them against the input row, preserving
// PostgreSQL's simultaneous assignment semantics (for example, SET (a, b) = (b, a)).
func nodeTupleUpdateExpr(ctx *Context, node *tree.UpdateExpr) (vitess.AssignmentExprs, error) {
	if _, ok := node.Expr.(*tree.Subquery); ok {
		return nil, errors.Errorf("tuple assignment from a subquery is not yet supported")
	}
	tuple, ok := node.Expr.(*tree.Tuple)
	if !ok {
		return nil, errors.Errorf("tuple assignment requires an explicit row expression")
	}
	if len(node.Names) != len(tuple.Exprs) {
		return nil, errors.Errorf("number of columns does not match number of values")
	}
	assignmentExprs := make(vitess.AssignmentExprs, len(node.Names))
	for i, name := range node.Names {
		expr, err := nodeExpr(ctx, tuple.Exprs[i])
		if err != nil {
			return nil, err
		}
		assignmentExprs[i] = &vitess.AssignmentExpr{
			Name: &vitess.ColName{Name: vitess.NewColIdent(string(name))},
			Expr: expr,
		}
	}
	return assignmentExprs, nil
}

// nodeUpdateExprs handles tree.UpdateExprs nodes.
func nodeUpdateExprs(ctx *Context, node tree.UpdateExprs) (vitess.AssignmentExprs, error) {
	if len(node) == 0 {
		return nil, nil
	}
	var assignmentExprs vitess.AssignmentExprs
	for i := range node {
		newAssignmentExprs, err := nodeUpdateExpr(ctx, node[i])
		if err != nil {
			return nil, err
		}
		assignmentExprs = append(assignmentExprs, newAssignmentExprs...)
	}
	return assignmentExprs, nil
}

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

package analyzer

import (
	"github.com/dolthub/go-mysql-server/sql"
	"github.com/dolthub/go-mysql-server/sql/analyzer"
	"github.com/dolthub/go-mysql-server/sql/plan"
	"github.com/dolthub/go-mysql-server/sql/transform"
)

// unwrapVirtualColumnTableForDropConstraint removes the projection-only table
// wrapper added for generated columns used by expression indexes. DROP
// CONSTRAINT neither reads nor evaluates those columns, and GMS's constraint
// resolver needs the underlying table's constraint interfaces.
func unwrapVirtualColumnTableForDropConstraint(
	ctx *sql.Context,
	_ *analyzer.Analyzer,
	n sql.Node,
	_ *plan.Scope,
	_ analyzer.RuleSelector,
	_ *sql.QueryFlags,
) (sql.Node, transform.TreeIdentity, error) {
	return transform.Node(ctx, n, func(ctx *sql.Context, candidate sql.Node) (sql.Node, transform.TreeIdentity, error) {
		drop, ok := candidate.(*plan.DropConstraint)
		if !ok {
			return candidate, transform.SameTree, nil
		}
		resolved, ok := drop.Child.(*plan.ResolvedTable)
		if !ok {
			return candidate, transform.SameTree, nil
		}
		virtual, ok := resolved.Table.(*plan.VirtualColumnTable)
		if !ok {
			return candidate, transform.SameTree, nil
		}

		updated := *resolved
		updated.Table = virtual.Underlying()
		replacement, err := drop.WithChildren(ctx, &updated)
		if err != nil {
			return nil, transform.SameTree, err
		}
		return replacement, transform.NewTree, nil
	})
}

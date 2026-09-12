// Copyright 2026 Dolthub, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package analyzer

import (
	"fmt"
	"strings"

	"github.com/dolthub/go-mysql-server/sql"
	"github.com/dolthub/go-mysql-server/sql/analyzer"
	"github.com/dolthub/go-mysql-server/sql/plan"
	"github.com/dolthub/go-mysql-server/sql/transform"
)

// prepareDropUniqueConstraintWithForeignKey preserves the implementation index
// required by Dolt for a declaring-side foreign key when PostgreSQL drops a
// UNIQUE constraint that currently supplies that index.
func prepareDropUniqueConstraintWithForeignKey(ctx *sql.Context, _ *analyzer.Analyzer, n sql.Node, _ *plan.Scope, _ analyzer.RuleSelector, _ *sql.QueryFlags) (sql.Node, transform.TreeIdentity, error) {
	return transform.Node(ctx, n, func(ctx *sql.Context, candidate sql.Node) (sql.Node, transform.TreeIdentity, error) {
		drop, ok := candidate.(*plan.DropConstraint)
		if !ok {
			return candidate, transform.SameTree, nil
		}
		rt, ok := drop.Child.(*plan.ResolvedTable)
		if !ok {
			return candidate, transform.SameTree, nil
		}
		table := sql.GetUnderlyingTable(rt.Table)
		indexed, ok := table.(sql.IndexAddressableTable)
		if !ok {
			return candidate, transform.SameTree, nil
		}
		indexes, err := indexed.GetIndexes(ctx)
		if err != nil {
			return nil, transform.SameTree, err
		}
		var dropped sql.Index
		usedNames := make(map[string]bool, len(indexes))
		for _, index := range indexes {
			usedNames[strings.ToLower(index.ID())] = true
			if strings.EqualFold(index.ID(), drop.Name) && index.IsUnique() {
				dropped = index
			}
		}
		if dropped == nil {
			return candidate, transform.SameTree, nil
		}
		fkTable, ok := table.(sql.ForeignKeyTable)
		if !ok {
			return candidate, transform.SameTree, nil
		}
		fks, err := fkTable.GetDeclaredForeignKeys(ctx)
		if err != nil {
			return nil, transform.SameTree, err
		}
		referencedFks, err := fkTable.GetReferencedForeignKeys(ctx)
		if err != nil {
			return nil, transform.SameTree, err
		}
		for _, fk := range referencedFks {
			if _, found, err := plan.FindFKIndexWithPrefix(ctx, fkTable, fk.ParentColumns, true, dropped.ID()); err != nil {
				return nil, transform.SameTree, err
			} else if !found {
				// The unique index is required on the referenced side. Preserve the
				// normal PostgreSQL error and do not create a child-side replacement.
				return candidate, transform.SameTree, nil
			}
		}
		var statements []sql.Node
		createdColumns := make(map[string]bool)
		for _, fk := range fks {
			if _, found, err := plan.FindFKIndexWithPrefix(ctx, fkTable, fk.Columns, false, dropped.ID()); err != nil {
				return nil, transform.SameTree, err
			} else if found {
				continue
			}
			columnKey := strings.Join(fk.Columns, "\x00")
			if createdColumns[columnKey] {
				continue
			}
			createdColumns[columnKey] = true
			name := fk.Name
			for suffix := 1; usedNames[strings.ToLower(name)]; suffix++ {
				name = fmt.Sprintf("%s_%d", fk.Name, suffix)
			}
			usedNames[strings.ToLower(name)] = true
			columns := make([]sql.IndexColumn, len(fk.Columns))
			for i, column := range fk.Columns {
				columns[i] = sql.IndexColumn{Name: column}
			}
			statements = append(statements, plan.NewAlterCreateIndex(rt.Database(), rt, false, name, sql.IndexUsing_Default, sql.IndexConstraint_None, columns, "", nil))
		}
		if len(statements) == 0 {
			return candidate, transform.SameTree, nil
		}
		statements = append(statements, plan.NewAlterDropIndex(rt.Database(), rt, drop.IfExists, dropped.ID()))
		return plan.NewBlock(statements), transform.NewTree, nil
	})
}

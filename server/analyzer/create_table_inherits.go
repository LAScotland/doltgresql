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
	"fmt"
	"strings"

	"github.com/dolthub/dolt/go/libraries/doltcore/doltdb"
	"github.com/dolthub/doltgresql/core"
	"github.com/dolthub/doltgresql/core/id"

	"github.com/dolthub/go-mysql-server/sql"
	"github.com/dolthub/go-mysql-server/sql/analyzer"
	"github.com/dolthub/go-mysql-server/sql/plan"
	"github.com/dolthub/go-mysql-server/sql/transform"

	pgast "github.com/dolthub/doltgresql/server/ast"
)

// normalizeCreateTableInherits removes the typed planning marker added by the
// PostgreSQL AST conversion and prevents GMS's CREATE TABLE LIKE behaviour from
// copying parent primary keys and indexes. PostgreSQL INHERITS retains column
// properties such as defaults and NOT NULL, but a child owns only keys it
// declares itself. Row visibility is handled by expandInheritedTables.
func normalizeCreateTableInherits(ctx *sql.Context, _ *analyzer.Analyzer, n sql.Node, _ *plan.Scope, _ analyzer.RuleSelector, _ *sql.QueryFlags) (sql.Node, transform.TreeIdentity, error) {
	ct, ok := n.(*plan.CreateTable)
	if !ok {
		return n, transform.SameTree, nil
	}

	pkSchema := ct.PkSchema()
	markerOrdinal := -1
	var metadata *pgast.InheritsMetadataExpression
	for i, column := range pkSchema.Schema {
		if column.Default == nil {
			continue
		}
		if candidate, ok := column.Default.Expr.(*pgast.InheritsMetadataExpression); ok {
			markerOrdinal = i
			metadata = candidate
			break
		}
	}
	if markerOrdinal < 0 {
		return n, transform.SameTree, nil
	}

	parents := make(pgast.ResolvedInheritanceParents, 0, len(metadata.Parents))
	seenParents := make(map[id.Table]bool)
	type inheritedColumn struct {
		name string
		typ  sql.Type
	}
	columnTypes := make(map[string]inheritedColumn)
	for _, column := range metadata.ChildColumns {
		columnTypes[strings.ToLower(column.Name)] = inheritedColumn{name: column.Name, typ: column.Type}
	}
	for _, parent := range metadata.Parents {
		if parent.Database != "" && parent.Database != ctx.GetCurrentDatabase() {
			return nil, transform.SameTree, fmt.Errorf("cross-database inheritance is not supported")
		}
		schemas := []string{parent.Schema}
		if parent.Schema == "" {
			var err error
			schemas, err = core.SearchPath(ctx)
			if err != nil {
				return nil, transform.SameTree, err
			}
		}
		found := false
		for _, schemaName := range schemas {
			table, err := core.GetSqlTableFromContext(ctx, "", doltdb.TableName{Name: parent.Name, Schema: schemaName})
			if err != nil {
				return nil, transform.SameTree, err
			}
			if table == nil {
				continue
			}
			parentID, ok, err := id.GetFromTable(ctx, table)
			if err != nil {
				return nil, transform.SameTree, err
			}
			if !ok {
				return nil, transform.SameTree, fmt.Errorf("inheritance requires a durable table")
			}
			if seenParents[parentID] {
				return nil, transform.SameTree, fmt.Errorf("relation %q would be inherited from more than once", parent.Name)
			}
			for _, column := range table.Schema(ctx) {
				key := strings.ToLower(column.Name)
				if existing, ok := columnTypes[key]; ok {
					if existing.name != column.Name {
						return nil, transform.SameTree, fmt.Errorf("inherited columns %q and %q differ only by case", existing.name, column.Name)
					}
					if !existing.typ.Equals(column.Type) {
						return nil, transform.SameTree, fmt.Errorf("inherited column %q has incompatible types", column.Name)
					}
				}
				columnTypes[key] = inheritedColumn{name: column.Name, typ: column.Type}
			}
			seenParents[parentID] = true
			parents = append(parents, parentID)
			found = true
			break
		}
		if !found {
			return nil, transform.SameTree, fmt.Errorf("inheritance parent %q does not exist", parent.Name)
		}
	}
	if ct.Temporary() && len(parents) > 0 {
		return nil, transform.SameTree, fmt.Errorf("temporary table inheritance is not supported")
	}
	options := make(map[string]interface{}, len(ct.TableOpts)+1)
	for k, v := range ct.TableOpts {
		options[k] = v
	}
	options[pgast.InheritanceTableOption] = parents
	schema := make(sql.Schema, 0, len(pkSchema.Schema)-1)
	for i, column := range pkSchema.Schema {
		if i == markerOrdinal {
			continue
		}
		copyColumn := *column
		copyColumn.PrimaryKey = false
		schema = append(schema, &copyColumn)
	}

	ordinals := make([]int, 0, len(metadata.PrimaryKeyColumns))
	seenPrimaryKeyColumns := make(map[string]struct{}, len(metadata.PrimaryKeyColumns))
	for _, pkName := range metadata.PrimaryKeyColumns {
		lowerName := strings.ToLower(pkName)
		if _, found := seenPrimaryKeyColumns[lowerName]; found {
			return nil, transform.SameTree, fmt.Errorf("primary key contains column %q more than once", pkName)
		}
		seenPrimaryKeyColumns[lowerName] = struct{}{}
		found := false
		for i, column := range schema {
			if strings.EqualFold(column.Name, pkName) {
				column.PrimaryKey = true
				column.Nullable = false
				ordinals = append(ordinals, i)
				found = true
				break
			}
		}
		if !found {
			return nil, transform.SameTree, sql.ErrKeyColumnDoesNotExist.New(pkName)
		}
	}

	targetName := metadata.TargetName
	if targetName == "" {
		targetName = ct.Name()
	}
	clean := plan.NewCreateTable(ct.Db, targetName, ct.IfNotExists(), ct.Temporary(), &plan.TableSpec{
		Schema:    sql.NewPrimaryKeySchema(schema, ordinals...),
		FkDefs:    ct.ForeignKeys(),
		ChDefs:    ct.Checks(),
		IdxDefs:   nil,
		Collation: ct.Collation,
		TableOpts: options,
	})
	if len(ct.ParentForeignKeyTables()) > 0 {
		var err error
		clean, err = clean.WithParentForeignKeyTables(ct.ParentForeignKeyTables())
		if err != nil {
			return nil, transform.SameTree, err
		}
	}
	return clean, transform.NewTree, nil
}

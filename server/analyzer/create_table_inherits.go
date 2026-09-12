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
// declares itself. This does not implement inherited row visibility.
func normalizeCreateTableInherits(_ *sql.Context, _ *analyzer.Analyzer, n sql.Node, _ *plan.Scope, _ analyzer.RuleSelector, _ *sql.QueryFlags) (sql.Node, transform.TreeIdentity, error) {
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

	clean := plan.NewCreateTable(ct.Db, ct.Name(), ct.IfNotExists(), ct.Temporary(), &plan.TableSpec{
		Schema:    sql.NewPrimaryKeySchema(schema, ordinals...),
		FkDefs:    ct.ForeignKeys(),
		ChDefs:    ct.Checks(),
		IdxDefs:   nil,
		Collation: ct.Collation,
		TableOpts: ct.TableOpts,
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

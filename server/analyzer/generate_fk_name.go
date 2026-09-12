// Copyright 2025 Dolthub, Inc.
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
	"github.com/dolthub/go-mysql-server/sql"
	"github.com/dolthub/go-mysql-server/sql/analyzer"
	"github.com/dolthub/go-mysql-server/sql/plan"
	"github.com/dolthub/go-mysql-server/sql/transform"

	"github.com/dolthub/doltgresql/core"
	"github.com/dolthub/doltgresql/server/functions"
)

// generateForeignKeyName populates a generated foreign key name, in the Postgres default foreign key name format,
// when a foreign key is created without an explicit name specified.
func generateForeignKeyName(ctx *sql.Context, _ *analyzer.Analyzer, n sql.Node, _ *plan.Scope, _ analyzer.RuleSelector, _ *sql.QueryFlags) (sql.Node, transform.TreeIdentity, error) {
	return populateForeignKeyDefaults(ctx, n, true)
}

// resolveImplicitForeignKeyColumns runs before GMS foreign-key validation, which requires concrete parent columns.
func resolveImplicitForeignKeyColumns(ctx *sql.Context, _ *analyzer.Analyzer, n sql.Node, _ *plan.Scope, _ analyzer.RuleSelector, _ *sql.QueryFlags) (sql.Node, transform.TreeIdentity, error) {
	return populateForeignKeyDefaults(ctx, n, false)
}

func populateForeignKeyDefaults(ctx *sql.Context, n sql.Node, generateNames bool) (sql.Node, transform.TreeIdentity, error) {
	return transform.Node(ctx, n, func(ctx *sql.Context, n sql.Node) (sql.Node, transform.TreeIdentity, error) {
		switch n := n.(type) {
		case *plan.CreateTable:
			copiedForeignKeys := make([]*sql.ForeignKeyConstraint, len(n.ForeignKeys()))
			for i := range n.ForeignKeys() {
				fk := *n.ForeignKeys()[i]
				copiedForeignKeys[i] = &fk
			}

			schemaName, err := core.GetSchemaName(ctx, n.Db, "")
			if err != nil {
				return nil, transform.SameTree, err
			}
			changedForeignKey := false
			for _, fk := range copiedForeignKeys {
				if len(fk.ParentColumns) == 0 {
					parentColumns, parentSchema, err := resolveImplicitForeignKeyParentColumns(ctx, fk, n.Name(), schemaName, n.PkSchema())
					if err != nil {
						return nil, transform.SameTree, err
					}
					fk.ParentColumns = parentColumns
					fk.ParentSchema = parentSchema
					changedForeignKey = true
				}
				if generateNames && fk.Name == "" {
					generatedName, err := generateFkName(ctx, n.Name(), fk)
					if err != nil {
						return nil, transform.SameTree, err
					}
					changedForeignKey = true
					fk.Name = generatedName
				}
				// A foreign key that references the table being created resolves both schemas to the table's schema
				if strings.EqualFold(fk.ParentTable, n.Name()) &&
					(fk.ParentSchema == "" || strings.EqualFold(fk.ParentSchema, schemaName)) &&
					(fk.SchemaName != schemaName || fk.ParentSchema != schemaName) {
					changedForeignKey = true
					fk.SchemaName = schemaName
					fk.ParentSchema = schemaName
				}
			}
			if changedForeignKey {
				newCreateTable := plan.NewCreateTable(n.Db, n.Name(), n.IfNotExists(), n.Temporary(), &plan.TableSpec{
					Schema:    n.PkSchema(),
					FkDefs:    copiedForeignKeys,
					ChDefs:    n.Checks(),
					IdxDefs:   n.Indexes(),
					Collation: n.Collation,
					TableOpts: n.TableOpts,
				})
				return newCreateTable, transform.NewTree, nil
			} else {
				return n, transform.SameTree, nil
			}

		case *plan.CreateForeignKey:
			if (generateNames && n.FkDef.Name == "") || len(n.FkDef.ParentColumns) == 0 {
				copiedFk := *n.FkDef
				if len(copiedFk.ParentColumns) == 0 {
					parentColumns, parentSchema, err := resolveImplicitForeignKeyParentColumns(ctx, &copiedFk, "", "", sql.PrimaryKeySchema{})
					if err != nil {
						return nil, transform.SameTree, err
					}
					copiedFk.ParentColumns = parentColumns
					copiedFk.ParentSchema = parentSchema
				}
				if generateNames && copiedFk.Name == "" {
					generatedName, err := generateFkName(ctx, copiedFk.Table, &copiedFk)
					if err != nil {
						return nil, transform.SameTree, err
					}
					copiedFk.Name = generatedName
				}
				return &plan.CreateForeignKey{
					DbProvider: n.DbProvider,
					FkDef:      &copiedFk,
				}, transform.NewTree, nil
			} else {
				return n, transform.SameTree, nil
			}

		default:
			return n, transform.SameTree, nil
		}
	})
}

// resolveImplicitForeignKeyParentColumns implements PostgreSQL's REFERENCES table shorthand by resolving it to the
// referenced table's primary key, preserving the primary key's declared column order. For CREATE TABLE, the table
// under construction participates at its normal position in the search path.
func resolveImplicitForeignKeyParentColumns(
	ctx *sql.Context,
	fk *sql.ForeignKeyConstraint,
	createdTableName string,
	createdTableSchema string,
	createdTablePkSchema sql.PrimaryKeySchema,
) ([]string, string, error) {
	var schemas []string
	if fk.ParentSchema != "" {
		schemas = []string{fk.ParentSchema}
	} else {
		var err error
		schemas, err = core.SearchPath(ctx)
		if err != nil {
			return nil, "", err
		}
	}

	for _, schemaName := range schemas {
		tbl, err := core.GetSqlTableFromContext(ctx, fk.ParentDatabase, doltdb.TableName{Name: fk.ParentTable, Schema: schemaName})
		if err != nil {
			return nil, "", err
		}
		if tbl != nil {
			return validateImplicitForeignKeyPrimaryKey(primaryKeyColumnNames(ctx, tbl), fk, schemaName)
		}
		if createdTableName != "" && strings.EqualFold(fk.ParentTable, createdTableName) && strings.EqualFold(schemaName, createdTableSchema) {
			return validateImplicitForeignKeyPrimaryKey(primaryKeyColumnNamesFromSchema(createdTablePkSchema), fk, schemaName)
		}
	}

	// Leave missing-table diagnostics to the normal analyzer path. This branch should only be reached for an invalid
	// reference, because a valid omitted-column reference must resolve to either an existing table or the table being
	// created.
	return nil, fk.ParentSchema, nil
}

func primaryKeyColumnNames(ctx *sql.Context, tbl sql.Table) []string {
	pkTable, ok := tbl.(sql.PrimaryKeyTable)
	if !ok {
		return nil
	}
	return primaryKeyColumnNamesFromSchema(pkTable.PrimaryKeySchema(ctx))
}

func primaryKeyColumnNamesFromSchema(pkSchema sql.PrimaryKeySchema) []string {
	if len(pkSchema.PkOrdinals) == 0 {
		return nil
	}
	columns := make([]string, len(pkSchema.PkOrdinals))
	for i, ordinal := range pkSchema.PkOrdinals {
		columns[i] = pkSchema.Schema[ordinal].Name
	}
	return columns
}

func validateImplicitForeignKeyPrimaryKey(columns []string, fk *sql.ForeignKeyConstraint, schemaName string) ([]string, string, error) {
	if len(columns) == 0 {
		return nil, "", fmt.Errorf(`there is no primary key for referenced table "%s"`, fk.ParentTable)
	}
	if len(fk.Columns) != len(columns) {
		return nil, "", fmt.Errorf("number of referencing and referenced columns for foreign key disagree")
	}
	return columns, schemaName, nil
}

// generateFkName creates a default foreign key name, according to Postgres naming rules
// (i.e. "<tablename>_<col1name>_<col2name>_fkey"). If an existing foreign key is found with the default, generated
// name, the generated name will be suffixed with a number to ensure uniqueness.
func generateFkName(ctx *sql.Context, tableName string, newFk *sql.ForeignKeyConstraint) (string, error) {
	columnNames := strings.Join(newFk.Columns, "_")
	generatedBaseName := fmt.Sprintf("%s_%s_fkey", tableName, columnNames)

	for counter := 0; counter < 100; counter += 1 {
		generatedFkName := generatedBaseName
		if counter > 0 {
			generatedFkName = fmt.Sprintf("%s%d", generatedBaseName, counter)
		}

		duplicate := false
		err := functions.IterateCurrentDatabase(ctx, functions.Callbacks{
			ForeignKey: func(ctx *sql.Context, schema functions.ItemSchema, table functions.ItemTable, foreignKey functions.ItemForeignKey) (cont bool, err error) {
				if foreignKey.Item.Name == generatedFkName {
					duplicate = true
					return false, nil
				}
				return true, nil
			},
		})
		if err != nil {
			return "", err
		}

		if !duplicate {
			return generatedFkName, nil
		}
	}

	return "", fmt.Errorf("unable to create unique foreign key %s: "+
		"a foreign key constraint already exists with this name", generatedBaseName)
}

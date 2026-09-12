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

package hook

import (
	"context"
	"fmt"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/dolthub/dolt/go/libraries/doltcore/doltdb"
	"github.com/dolthub/go-mysql-server/sql"
	"github.com/dolthub/go-mysql-server/sql/plan"

	"github.com/dolthub/doltgresql/core"
	"github.com/dolthub/doltgresql/core/id"
	pgtypes "github.com/dolthub/doltgresql/server/types"
)

// BeforeTableAddColumn handles validation that's unique to Doltgres.
func BeforeTableAddColumn(ctx *sql.Context, runner sql.StatementRunner, nodeInterface sql.Node) (sql.Node, error) {
	n, ok := nodeInterface.(*plan.AddColumn)
	if !ok {
		return nil, errors.Errorf("ADD COLUMN pre-hook expected `*plan.AddColumn` but received `%T`", nodeInterface)
	}
	// Grab the table being altered
	doltTable := core.SQLNodeToDoltTable(n.Table)
	if doltTable == nil {
		// If this table isn't a Dolt table then we don't have anything to do
		return n, nil
	}
	if !isInheritancePropagation(ctx) {
		if err := validateInheritedAddColumn(ctx, n, doltTable.TableName()); err != nil {
			return nil, err
		}
	}
	// If the column being added doesn't have a default value, then the row-type checks below have nothing to do.
	if n.Column().Default == nil {
		return n, nil
	}
	_, root, err := core.GetRootFromContext(ctx)
	if err != nil {
		return n, nil
	}
	tableName := doltTable.TableName()
	tableAsType := id.NewType(tableName.Schema, tableName.Name)
	allTableNames, err := root.GetAllTableNames(ctx, false)
	if err != nil {
		return nil, err
	}

	for _, otherTableName := range allTableNames {
		if doltdb.IsSystemTable(otherTableName) {
			// System tables don't use any table types
			continue
		}
		otherTable, ok, err := root.GetTable(ctx, otherTableName)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errors.Errorf("root returned table name `%s` but it could not be found?", otherTableName.String())
		}
		otherTableSch, err := otherTable.GetSchema(ctx)
		if err != nil {
			return nil, err
		}
		for _, otherCol := range otherTableSch.GetAllCols().GetColumns() {
			colType := otherCol.TypeInfo.ToSqlType()
			dgtype, ok := colType.(*pgtypes.DoltgresType)
			if !ok {
				// If this isn't a Doltgres type, then it can't be a table type so we can ignore it
				continue
			}
			if dgtype.ID != tableAsType {
				// This column isn't our table type, so we can ignore it
				continue
			}
			return nil, errors.Errorf(`cannot alter table "%s" because column "%s.%s" uses its row type`,
				tableName.Name, otherTableName.Name, otherCol.Name)
		}
	}
	return n, nil
}

type inheritancePropagationKey struct{}

type inheritedAddColumnSnapshotKey struct {
	node *plan.AddColumn
}

type inheritedAddColumnSnapshot struct {
	database string
	root     doltdb.RootValue
}

func isInheritancePropagation(ctx *sql.Context) bool {
	active, _ := ctx.Value(inheritancePropagationKey{}).(bool)
	return active
}

func validateInheritedAddColumn(ctx *sql.Context, n *plan.AddColumn, tableName doltdb.TableName) error {
	collection, err := core.GetInheritanceCollectionFromContext(ctx, ctx.GetCurrentDatabase())
	if err != nil {
		return err
	}
	descendants := collection.Descendants(ctx, id.NewTable(tableName.Schema, tableName.Name))
	if len(descendants) == 0 {
		return nil
	}
	column := n.Column()
	if !column.Nullable || column.Default != nil || column.Generated != nil || n.Order(ctx) != nil {
		return errors.Errorf("adding column %q to inherited table %q is only supported for nullable columns without defaults or position clauses", column.Name, tableName.Name)
	}
	dgType, ok := column.Type.(*pgtypes.DoltgresType)
	if !ok || !isSupportedInheritedColumnType(dgType) {
		return errors.Errorf("adding column %q of type %s to inherited table %q is not yet supported", column.Name, column.Type.String(), tableName.Name)
	}
	for _, descendant := range descendants {
		tbl, err := core.GetSqlTableFromContext(ctx, ctx.GetCurrentDatabase(), doltdb.TableName{Schema: descendant.SchemaName(), Name: descendant.TableName()})
		if err != nil {
			return err
		}
		if tbl == nil {
			return errors.Errorf("inheritance metadata references missing table %q", descendant.TableName())
		}
		if tbl.Schema(ctx).IndexOfColName(column.Name) >= 0 {
			return errors.Errorf("cannot propagate inherited column %q to table %q because that column already exists", column.Name, descendant.TableName())
		}
	}
	_, root, err := core.GetRootFromContext(ctx)
	if err != nil {
		return err
	}
	// The post-hook performs several physical ALTER statements. Keep the root
	// from before the parent alteration so a later nested failure cannot leave a
	// partially propagated hierarchy visible, even if a context finalizer runs
	// before the connection-level transaction rollback.
	ctx.Context = context.WithValue(ctx.Context, inheritedAddColumnSnapshotKey{node: n}, inheritedAddColumnSnapshot{
		database: ctx.GetCurrentDatabase(),
		root:     root,
	})
	return nil
}

func isSupportedInheritedColumnType(t *pgtypes.DoltgresType) bool {
	switch t.ID {
	case pgtypes.Int16.ID, pgtypes.Int32.ID, pgtypes.Int64.ID, pgtypes.Text.ID, pgtypes.VarChar.ID, pgtypes.JsonB.ID, pgtypes.Timestamp.ID:
		return true
	default:
		return false
	}
}

func propagateInheritedAddColumn(ctx *sql.Context, runner sql.StatementRunner, n *plan.AddColumn, tableName doltdb.TableName) error {
	collection, err := core.GetInheritanceCollectionFromContext(ctx, ctx.GetCurrentDatabase())
	if err != nil {
		return err
	}
	descendants := collection.Descendants(ctx, id.NewTable(tableName.Schema, tableName.Name))
	if len(descendants) == 0 {
		return nil
	}
	column := n.Column()
	for _, descendant := range descendants {
		alterStatement := fmt.Sprintf("ALTER TABLE %s.%s ADD COLUMN %s %s",
			quoteIdentifier(descendant.SchemaName()), quoteIdentifier(descendant.TableName()),
			quoteIdentifier(column.Name), column.Type.String())
		_, err = sql.RunInterpreted(ctx, func(subCtx *sql.Context) ([]sql.Row, error) {
			guardedCtx := subCtx.WithContext(context.WithValue(subCtx.Context, inheritancePropagationKey{}, true))
			_, rowIter, _, err := runner.QueryWithBindings(guardedCtx, alterStatement, nil, nil, nil)
			if err != nil {
				return nil, err
			}
			return sql.RowIterToRows(guardedCtx, rowIter)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// AfterTableAddColumn handles updating various table columns, alongside other validation that's unique to Doltgres.
func AfterTableAddColumn(ctx *sql.Context, runner sql.StatementRunner, nodeInterface sql.Node) (retErr error) {
	n, ok := nodeInterface.(*plan.AddColumn)
	if !ok {
		return errors.Errorf("ADD COLUMN post-hook expected `*plan.AddColumn` but received `%T`", nodeInterface)
	}

	// Grab the table being altered
	doltTable := core.SQLNodeToDoltTable(n.Table)
	if doltTable == nil {
		// If this table isn't a Dolt table then we don't have anything to do
		return nil
	}
	if snapshot, ok := ctx.Value(inheritedAddColumnSnapshotKey{node: n}).(inheritedAddColumnSnapshot); ok {
		defer func() {
			if retErr == nil {
				return
			}
			session, _, rootErr := core.GetRootFromContext(ctx)
			if rootErr == nil {
				rootErr = session.SetWorkingRoot(ctx, snapshot.database, snapshot.root)
			}
			if rootErr != nil {
				retErr = errors.WithSecondaryError(retErr, errors.Wrap(rootErr, "failed to restore inherited ADD COLUMN root"))
			}
		}()
	}
	_, root, err := core.GetRootFromContext(ctx)
	if err != nil {
		return err
	}
	tableName := doltTable.TableName()
	if !isInheritancePropagation(ctx) {
		if err := propagateInheritedAddColumn(ctx, runner, n, tableName); err != nil {
			return err
		}
	}
	tableAsType := id.NewType(tableName.Schema, tableName.Name)
	allTableNames, err := root.GetAllTableNames(ctx, false)
	if err != nil {
		return err
	}
	sch := doltTable.Schema(ctx)

	for _, otherTableName := range allTableNames {
		if doltdb.IsSystemTable(otherTableName) {
			// System tables don't use any table types
			continue
		}
		otherTable, ok, err := root.GetTable(ctx, otherTableName)
		if err != nil {
			return err
		}
		if !ok {
			return errors.Errorf("root returned table name `%s` but it could not be found?", otherTableName.String())
		}
		otherTableSch, err := otherTable.GetSchema(ctx)
		if err != nil {
			return err
		}
		for _, otherCol := range otherTableSch.GetAllCols().GetColumns() {
			colType := otherCol.TypeInfo.ToSqlType()
			dgtype, ok := colType.(*pgtypes.DoltgresType)
			if !ok {
				// If this isn't a Doltgres type, then it can't be a table type so we can ignore it
				continue
			}
			if dgtype.ID != tableAsType {
				// This column isn't our table type, so we can ignore it
				continue
			}
			// Build the UPDATE statement that we'll run for this table
			rowValues := make([]string, len(sch)+1)
			for i, col := range sch {
				rowValues[i] = fmt.Sprintf(`("%s")."%s"`, otherCol.Name, col.Name)
			}
			rowValues[len(rowValues)-1] = "NULL"
			// The UPDATE changes the values in the table
			updateStr := fmt.Sprintf(`UPDATE "%s"."%s" SET "%s" = ROW(%s)::"%s"."%s" WHERE length("%s"::text) > 0;`,
				otherTableName.Schema, otherTableName.Name, otherCol.Name, strings.Join(rowValues, ","), tableName.Schema, tableName.Name, otherCol.Name)
			// The ALTER updates the type on the schema since it still has the old one
			alterStr := fmt.Sprintf(`ALTER TABLE "%s"."%s" ALTER COLUMN "%s" TYPE "%s"."%s";`,
				otherTableName.Schema, otherTableName.Name, otherCol.Name, tableName.Schema, tableName.Name)
			// We run the statements as though they were interpreted since we're running new statements inside the original
			_, err = sql.RunInterpreted(ctx, func(subCtx *sql.Context) ([]sql.Row, error) {
				_, rowIter, _, err := runner.QueryWithBindings(subCtx, updateStr, nil, nil, nil)
				if err != nil {
					return nil, err
				}
				_, err = sql.RowIterToRows(subCtx, rowIter)
				if err != nil {
					return nil, err
				}
				_, rowIter, _, err = runner.QueryWithBindings(subCtx, alterStr, nil, nil, nil)
				if err != nil {
					return nil, err
				}
				return sql.RowIterToRows(subCtx, rowIter)
			})
			if err != nil {
				return err
			}
		}
	}
	return nil
}

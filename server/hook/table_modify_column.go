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
	"io"

	"github.com/cockroachdb/errors"
	"github.com/dolthub/dolt/go/libraries/doltcore/doltdb"
	"github.com/dolthub/go-mysql-server/sql"
	"github.com/dolthub/go-mysql-server/sql/plan"

	"github.com/dolthub/doltgresql/core"
	"github.com/dolthub/doltgresql/core/id"
	pgtypes "github.com/dolthub/doltgresql/server/types"
)

type inheritedNotNullSnapshotKey struct{ node *plan.ModifyColumn }

type inheritedNotNullSnapshot struct {
	database string
	root     doltdb.RootValue
	table    doltdb.TableName
	column   string
}

// beforeTableModifyColumnChange represents what properties of a column changed when a call is made to BeforeTableModifyColumn.
type beforeTableModifyColumnChange uint8

const (
	beforeTableModifyColumnChange_None beforeTableModifyColumnChange = iota
	beforeTableModifyColumnChange_Type
	beforeTableModifyColumnChange_Nullability
)

// BeforeTableModifyColumn handles validation that's unique to Doltgres.
func BeforeTableModifyColumn(ctx *sql.Context, runner sql.StatementRunner, nodeInterface sql.Node) (sql.Node, error) {
	n, ok := nodeInterface.(*plan.ModifyColumn)
	if !ok {
		return nil, errors.Errorf("MODIFY COLUMN pre-hook expected `*plan.ModifyColumn` but received `%T`", nodeInterface)
	}

	// Figure out what was changed. We know it's not the name because we have a dedicated *RenameColumn node.
	changed := beforeTableModifyColumnChange_None
	newColumn := n.NewColumn()
	for _, col := range n.TargetSchema() {
		if col.Name == newColumn.Name {
			if !col.Type.Equals(newColumn.Type) {
				changed = beforeTableModifyColumnChange_Type
			} else if col.Nullable != newColumn.Nullable {
				changed = beforeTableModifyColumnChange_Nullability
			}
		}
	}
	if changed == beforeTableModifyColumnChange_None {
		return n, nil
	}

	// Grab the table being altered (so we know the schema)
	doltTable := core.SQLNodeToDoltTable(n.Table)
	if doltTable == nil {
		// If this table isn't a Dolt table then we don't have anything to do
		return n, nil
	}
	if changed == beforeTableModifyColumnChange_Type {
		if err := RejectInheritedColumnMutation(ctx, "alter the type of", newColumn.Name, doltTable.TableName()); err != nil {
			return nil, err
		}
		if err := ValidateColumnTypeChangeForTable(ctx, doltTable.TableName()); err != nil {
			return nil, err
		}
		return n, nil
	}
	if isInheritancePropagation(ctx) {
		return n, nil
	}
	if newColumn.Nullable {
		if err := RejectInheritedColumnMutation(ctx, "drop NOT NULL from", newColumn.Name, doltTable.TableName()); err != nil {
			return nil, err
		}
		return n, nil
	}
	if err := prepareInheritedSetNotNull(ctx, runner, n, doltTable.TableName(), newColumn.Name); err != nil {
		return nil, err
	}
	return n, nil
}

func prepareInheritedSetNotNull(ctx *sql.Context, runner sql.StatementRunner, n *plan.ModifyColumn, tableName doltdb.TableName, columnName string) error {
	collection, err := core.GetInheritanceCollectionFromContext(ctx, ctx.GetCurrentDatabase())
	if err != nil {
		return err
	}
	descendants := collection.Descendants(ctx, id.NewTable(tableName.Schema, tableName.Name))
	if len(descendants) == 0 {
		return nil
	}
	tables := make([]id.Table, 0, len(descendants)+1)
	tables = append(tables, id.NewTable(tableName.Schema, tableName.Name))
	tables = append(tables, descendants...)
	for _, tableID := range tables {
		hasNull, err := inheritedColumnHasNull(ctx, runner, tableID, columnName)
		if err != nil {
			return err
		}
		if hasNull {
			return errors.Errorf("column %q of table %q contains null values", columnName, tableID.TableName())
		}
	}
	_, root, err := core.GetRootFromContext(ctx)
	if err != nil {
		return err
	}
	ctx.Context = context.WithValue(ctx.Context, inheritedNotNullSnapshotKey{node: n}, inheritedNotNullSnapshot{
		database: ctx.GetCurrentDatabase(), root: root, table: tableName, column: columnName,
	})
	return nil
}

func inheritedColumnHasNull(ctx *sql.Context, runner sql.StatementRunner, table id.Table, columnName string) (bool, error) {
	query := fmt.Sprintf("SELECT 1 FROM ONLY %s.%s WHERE %s IS NULL LIMIT 1",
		quoteIdentifier(table.SchemaName()), quoteIdentifier(table.TableName()), quoteIdentifier(columnName))
	rows, err := sql.RunInterpreted(ctx, func(subCtx *sql.Context) ([]sql.Row, error) {
		_, iter, _, err := runner.QueryWithBindings(subCtx, query, nil, nil, nil)
		if err != nil {
			return nil, err
		}
		defer iter.Close(subCtx)
		row, err := iter.Next(subCtx)
		if err == io.EOF {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return []sql.Row{row}, nil
	})
	return len(rows) > 0, err
}

// AfterTableModifyColumn propagates a validated SET NOT NULL through descendants.
func AfterTableModifyColumn(ctx *sql.Context, runner sql.StatementRunner, nodeInterface sql.Node) (retErr error) {
	n, ok := nodeInterface.(*plan.ModifyColumn)
	if !ok {
		return errors.Errorf("MODIFY COLUMN post-hook expected `*plan.ModifyColumn` but received `%T`", nodeInterface)
	}
	if isInheritancePropagation(ctx) {
		return nil
	}
	snapshot, ok := ctx.Value(inheritedNotNullSnapshotKey{node: n}).(inheritedNotNullSnapshot)
	if !ok {
		return nil
	}
	defer func() {
		if retErr == nil {
			return
		}
		session, _, rootErr := core.GetRootFromContext(ctx)
		if rootErr == nil {
			rootErr = session.SetWorkingRoot(ctx, snapshot.database, snapshot.root)
		}
		if rootErr != nil {
			retErr = errors.WithSecondaryError(retErr, errors.Wrap(rootErr, "failed to restore inherited SET NOT NULL root"))
		}
	}()
	collection, err := core.GetInheritanceCollectionFromContext(ctx, snapshot.database)
	if err != nil {
		return err
	}
	for _, descendant := range collection.Descendants(ctx, id.NewTable(snapshot.table.Schema, snapshot.table.Name)) {
		statement := fmt.Sprintf("ALTER TABLE %s.%s ALTER COLUMN %s SET NOT NULL",
			quoteIdentifier(descendant.SchemaName()), quoteIdentifier(descendant.TableName()), quoteIdentifier(snapshot.column))
		_, err = sql.RunInterpreted(ctx, func(subCtx *sql.Context) ([]sql.Row, error) {
			guardedCtx := subCtx.WithContext(context.WithValue(subCtx.Context, inheritancePropagationKey{}, true))
			_, iter, _, err := runner.QueryWithBindings(guardedCtx, statement, nil, nil, nil)
			if err != nil {
				return nil, err
			}
			return sql.RowIterToRows(guardedCtx, iter)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// ValidateColumnTypeChangeForTable returns an error if the given table's implicit row type is used as the type of a
// column in any other table, which prevents altering the table's column types.
func ValidateColumnTypeChangeForTable(ctx *sql.Context, tableName doltdb.TableName) error {
	_, root, err := core.GetRootFromContext(ctx)
	if err != nil {
		return nil
	}
	tableAsType := id.NewType(tableName.Schema, tableName.Name)
	allTableNames, err := root.GetAllTableNames(ctx, false)
	if err != nil {
		return err
	}

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
			return errors.Errorf(`cannot alter table "%s" because column "%s.%s" uses its row type`,
				tableName.Name, otherTableName.Name, otherCol.Name)
		}
	}
	return nil
}

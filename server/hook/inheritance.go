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
	"github.com/cockroachdb/errors"
	"github.com/dolthub/dolt/go/libraries/doltcore/doltdb"
	"github.com/dolthub/go-mysql-server/sql"

	"github.com/dolthub/doltgresql/core"
	"github.com/dolthub/doltgresql/core/id"
)

func rejectInheritanceTableMutation(ctx *sql.Context, operation string, tableName doltdb.TableName) error {
	collection, err := core.GetInheritanceCollectionFromContext(ctx, ctx.GetCurrentDatabase())
	if err != nil {
		return err
	}
	tableID := id.NewTable(tableName.Schema, tableName.Name)
	if len(collection.GetParents(ctx, tableID)) == 0 && len(collection.Descendants(ctx, tableID)) == 0 {
		return nil
	}
	return errors.Errorf("cannot %s table %q while it participates in table inheritance", operation, tableName.Name)
}

// RejectInheritedColumnMutation rejects changes to a column inherited from an ancestor or inherited by a descendant.
// It remains exported for custom ALTER COLUMN nodes that do not pass through the standard execution hooks.
func RejectInheritedColumnMutation(ctx *sql.Context, operation, columnName string, tableName doltdb.TableName) error {
	collection, err := core.GetInheritanceCollectionFromContext(ctx, ctx.GetCurrentDatabase())
	if err != nil {
		return err
	}
	tableID := id.NewTable(tableName.Schema, tableName.Name)
	if len(collection.Descendants(ctx, tableID)) > 0 {
		return errors.Errorf("cannot %s column %q of table %q because it is inherited by another table", operation, columnName, tableName.Name)
	}
	seen := make(map[id.Table]struct{})
	parents := collection.GetParents(ctx, tableID)
	for len(parents) > 0 {
		parent := parents[0]
		parents = parents[1:]
		if _, ok := seen[parent]; ok {
			continue
		}
		seen[parent] = struct{}{}
		tbl, err := core.GetSqlTableFromContext(ctx, ctx.GetCurrentDatabase(), doltdb.TableName{Schema: parent.SchemaName(), Name: parent.TableName()})
		if err != nil {
			return err
		}
		if tbl == nil {
			return errors.Errorf("inheritance metadata references missing table %q", parent.TableName())
		}
		if tbl.Schema(ctx).IndexOfColName(columnName) >= 0 {
			return errors.Errorf("cannot %s inherited column %q of table %q", operation, columnName, tableName.Name)
		}
		parents = append(parents, collection.GetParents(ctx, parent)...)
	}
	return nil
}

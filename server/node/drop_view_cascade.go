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

package node

import (
	"context"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/dolthub/dolt/go/libraries/doltcore/doltdb"
	"github.com/dolthub/go-mysql-server/sql"
	"github.com/dolthub/go-mysql-server/sql/plan"
	vitess "github.com/dolthub/vitess/go/vt/sqlparser"

	"github.com/dolthub/doltgresql/core"
	"github.com/dolthub/doltgresql/server/hook"
)

type DropViewName struct {
	Schema string
	Name   string
}

// DropViewCascade implements DROP VIEW ... CASCADE using Doltgres' parsed view dependency graph.
type DropViewCascade struct {
	names    []DropViewName
	ifExists bool
}

var _ sql.ExecSourceRel = (*DropViewCascade)(nil)
var _ vitess.Injectable = (*DropViewCascade)(nil)

func NewDropViewCascade(names []DropViewName, ifExists bool) *DropViewCascade {
	return &DropViewCascade{names: names, ifExists: ifExists}
}

func (d *DropViewCascade) Children() []sql.Node           { return nil }
func (d *DropViewCascade) IsReadOnly() bool               { return false }
func (d *DropViewCascade) Resolved() bool                 { return true }
func (d *DropViewCascade) Schema(*sql.Context) sql.Schema { return nil }
func (d *DropViewCascade) String() string                 { return "DROP VIEW CASCADE" }
func (d *DropViewCascade) WithChildren(ctx *sql.Context, children ...sql.Node) (sql.Node, error) {
	return plan.NillaryWithChildren(d, children...)
}
func (d *DropViewCascade) WithResolvedChildren(ctx context.Context, children []any) (any, error) {
	if len(children) != 0 {
		return nil, ErrVitessChildCount.New(0, len(children))
	}
	return d, nil
}

type resolvedDropView struct {
	name doltdb.TableName
	db   sql.ViewDatabase
}

func (d *DropViewCascade) RowIter(ctx *sql.Context, _ sql.Row) (_ sql.RowIter, retErr error) {
	db, err := core.GetSqlDatabaseFromContext(ctx, "")
	if err != nil {
		return nil, err
	}
	schemaDb, ok := db.(sql.SchemaDatabase)
	if !ok {
		return nil, errors.Errorf("database does not support schemas")
	}
	currentSchema, err := core.GetCurrentSchema(ctx)
	if err != nil {
		return nil, err
	}

	// Resolve every requested relation before mutating anything. This preserves statement atomicity for missing
	// relations, wrong relation types, and unsupported schemas in a multi-view DROP.
	resolved := make([]resolvedDropView, 0, len(d.names))
	resolvedKeys := make(map[string]struct{}, len(d.names))
	for _, requested := range d.names {
		// Authorization binds an unqualified identifier to the current schema. Execution must use the same binding;
		// searching later schemas could authorize one schema and mutate another.
		schemas := []string{currentSchema}
		if requested.Schema != "" {
			schemas = []string{requested.Schema}
		}
		var found bool
		for _, schemaName := range schemas {
			schema, exists, err := schemaDb.GetSchema(ctx, schemaName)
			if err != nil {
				return nil, err
			}
			if !exists {
				continue
			}
			if table, _, err := schema.GetTableInsensitive(ctx, requested.Name); err != nil {
				return nil, err
			} else if table != nil {
				return nil, errors.Errorf(`"%s" is not a view`, requested.Name)
			}
			viewDb, ok := schema.(sql.ViewDatabase)
			if !ok {
				continue
			}
			definition, exists, err := viewDb.GetViewDefinition(ctx, requested.Name)
			if err != nil {
				return nil, err
			}
			if exists {
				key := strings.ToLower(schemaName) + "\x00" + strings.ToLower(definition.Name)
				if _, duplicate := resolvedKeys[key]; duplicate {
					found = true
					break
				}
				resolvedKeys[key] = struct{}{}
				resolved = append(resolved, resolvedDropView{
					name: doltdb.TableName{Schema: schemaName, Name: definition.Name}, db: viewDb,
				})
				found = true
				break
			}
		}
		if !found && !d.ifExists {
			return nil, errors.Errorf(`view "%s" does not exist`, requested.Name)
		}
	}
	if len(resolved) == 0 {
		return sql.RowsToRowIter(), nil
	}

	roots := make([]doltdb.TableName, len(resolved))
	for i := range resolved {
		roots[i] = resolved[i].name
	}
	dependents, err := hook.DependentViewsForDropView(ctx, roots)
	if err != nil {
		return nil, err
	}
	if len(dependents) != 0 {
		return nil, errors.Errorf("DROP VIEW CASCADE with dependent views is not yet supported")
	}
	session, root, err := core.GetRootFromContext(ctx)
	if err != nil {
		return nil, err
	}
	databaseName := ctx.GetCurrentDatabase()
	defer func() {
		if retErr == nil {
			return
		}
		if restoreErr := session.SetWorkingRoot(ctx, databaseName, root); restoreErr != nil {
			retErr = errors.WithSecondaryError(retErr, errors.Wrap(restoreErr, "failed to restore DROP VIEW CASCADE root"))
		}
	}()
	for _, view := range resolved {
		if err = view.db.DropView(ctx, view.name.Name); err != nil {
			return nil, err
		}
	}
	return sql.RowsToRowIter(), nil
}

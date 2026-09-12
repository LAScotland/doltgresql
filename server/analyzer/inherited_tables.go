// Copyright 2026 Dolthub, Inc.
// Licensed under the Apache License, Version 2.0 (the "License").
package analyzer

import (
	"fmt"
	"github.com/dolthub/dolt/go/libraries/doltcore/doltdb"
	"github.com/dolthub/doltgresql/core"
	"github.com/dolthub/doltgresql/core/id"
	pgast "github.com/dolthub/doltgresql/server/ast"
	pgnode "github.com/dolthub/doltgresql/server/node"
	"github.com/dolthub/go-mysql-server/sql"
	"github.com/dolthub/go-mysql-server/sql/analyzer"
	"github.com/dolthub/go-mysql-server/sql/plan"
	"github.com/dolthub/go-mysql-server/sql/transform"
)

// onlyPhysicalTable deliberately carries no descendant scanning behavior.
type onlyPhysicalTable struct{ sql.Table }

func expandInheritedTables(ctx *sql.Context, _ *analyzer.Analyzer, n sql.Node, _ *plan.Scope, _ analyzer.RuleSelector, flags *sql.QueryFlags) (sql.Node, transform.TreeIdentity, error) {
	// Consume typed ONLY markers before any simplifier can erase their meaning.
	n, marked, err := transform.NodeWithOpaque(ctx, n, func(ctx *sql.Context, node sql.Node) (sql.Node, transform.TreeIdentity, error) {
		filter, ok := node.(*plan.Filter)
		if !ok {
			return node, transform.SameTree, nil
		}
		if _, ok := filter.Expression.(*pgast.OnlyTableMarkerExpression); !ok {
			return node, transform.SameTree, nil
		}
		child, _, err := transform.NodeWithOpaque(ctx, filter.Child, func(ctx *sql.Context, node sql.Node) (sql.Node, transform.TreeIdentity, error) {
			rt, ok := node.(*plan.ResolvedTable)
			if !ok {
				return node, transform.SameTree, nil
			}
			copy := *rt
			copy.Table = &onlyPhysicalTable{rt.Table}
			return &copy, transform.NewTree, nil
		})
		return child, transform.NewTree, err
	})
	if err != nil {
		return nil, transform.SameTree, err
	}
	if err := validateInheritanceDdl(ctx, n); err != nil {
		return nil, transform.SameTree, err
	}
	if err := validateInheritanceMutations(ctx, n); err != nil {
		return nil, transform.SameTree, err
	}
	if flags.IsSet(sql.QFlagDDL) || flags.IsSet(sql.QFlagAlterTable) {
		return n, marked, nil
	}
	// INSERT's destination is a physical relation, even when it is a parent.
	destinations := make(map[*plan.ResolvedTable]bool)
	transform.Inspect(n, func(node sql.Node) bool {
		if ins, ok := node.(*plan.InsertInto); ok {
			transform.Inspect(ins.Destination, func(node sql.Node) bool {
				if rt, ok := node.(*plan.ResolvedTable); ok {
					destinations[rt] = true
				}
				return true
			})
		}
		return true
	})
	expanded, changed, err := transform.NodeWithOpaque(ctx, n, func(ctx *sql.Context, node sql.Node) (sql.Node, transform.TreeIdentity, error) {
		rt, ok := node.(*plan.ResolvedTable)
		if !ok || destinations[rt] {
			return node, transform.SameTree, nil
		}
		switch rt.Table.(type) {
		case *pgnode.InheritedTable, *onlyPhysicalTable:
			return node, transform.SameTree, nil
		}
		physical := core.SQLTableToDoltTable(rt.Table)
		if physical == nil {
			return node, transform.SameTree, nil
		}
		name := physical.TableName()
		parent := id.NewTable(name.Schema, name.Name)
		coll, err := core.GetInheritanceCollectionFromContext(ctx, "")
		if err != nil {
			return nil, transform.SameTree, err
		}
		descendants := coll.Descendants(ctx, parent)
		if len(descendants) == 0 {
			return node, transform.SameTree, nil
		}
		if flags.IsSet(sql.QFlagUpdate) || flags.IsSet(sql.QFlagDelete) {
			return nil, transform.SameTree, fmt.Errorf("inherited UPDATE/DELETE is not yet supported for table %q", name.Name)
		}
		if rt.AsOf != nil {
			return nil, transform.SameTree, fmt.Errorf("historical inherited-table reads are not yet supported")
		}
		children := make([]sql.Table, 0, len(descendants))
		for _, child := range descendants {
			table, err := core.GetSqlTableFromContext(ctx, "", doltdb.TableName{Schema: child.SchemaName(), Name: child.TableName()})
			if err != nil {
				return nil, transform.SameTree, err
			}
			if table == nil {
				return nil, transform.SameTree, fmt.Errorf("inheritance metadata references missing table %s.%s", child.SchemaName(), child.TableName())
			}
			children = append(children, table)
		}
		scan, err := pgnode.NewInheritedTable(ctx, rt.Table, children)
		if err != nil {
			return nil, transform.SameTree, err
		}
		copy := *rt
		copy.Table = scan
		return &copy, transform.NewTree, nil
	})
	return expanded, marked && changed, err
}

func validateInheritedTableMutations(ctx *sql.Context, _ *analyzer.Analyzer, n sql.Node, _ *plan.Scope, _ analyzer.RuleSelector, _ *sql.QueryFlags) (sql.Node, transform.TreeIdentity, error) {
	return n, transform.SameTree, validateInheritanceMutations(ctx, n)
}

// validateInheritanceMutations checks the statement's mutation target directly.
// Query flags are not populated consistently at this early analyzer stage, and
// expanding a parent scan underneath an UpdateSource or DeleteFrom would let the
// row editor mutate only part of the inherited relation.
func validateInheritanceMutations(ctx *sql.Context, n sql.Node) error {
	var result error
	transform.Inspect(n, func(node sql.Node) bool {
		if result != nil {
			return false
		}
		var targets []sql.Node
		switch mutation := node.(type) {
		case *plan.Update:
			if source, ok := mutation.Child.(*plan.UpdateSource); ok {
				targets = []sql.Node{source.Child}
			} else {
				targets = []sql.Node{mutation.Child}
			}
		case *plan.DeleteFrom:
			targets = mutation.GetDeleteTargets()
			if len(targets) == 0 {
				targets = []sql.Node{mutation.Child}
			}
		default:
			return true
		}
		for _, target := range targets {
			transform.Inspect(target, func(child sql.Node) bool {
				if result != nil {
					return false
				}
				rt, ok := child.(*plan.ResolvedTable)
				if !ok {
					return true
				}
				table := core.SQLTableToDoltTable(rt.Table)
				if table == nil {
					return true
				}
				name := table.TableName()
				coll, err := core.GetInheritanceCollectionFromContext(ctx, "")
				if err != nil {
					result = err
					return false
				}
				if len(coll.Descendants(ctx, id.NewTable(name.Schema, name.Name))) > 0 {
					result = fmt.Errorf("inherited UPDATE/DELETE is not yet supported for table %q", name.Name)
				}
				// The first resolved table is the UPDATE/DELETE target. Other tables
				// below a join or subquery are read sources and must not be rejected.
				return false
			})
		}
		return false
	})
	return result
}

func validateInheritanceDdl(ctx *sql.Context, n sql.Node) error {
	var result error
	transform.Inspect(n, func(node sql.Node) bool {
		if result != nil {
			return false
		}
		var target sql.Node
		allParticipants := false
		switch t := node.(type) {
		case *plan.Truncate:
			target = t.Child
		case *plan.AlterDefaultSet:
			target = t.Table
		case *plan.AlterDefaultDrop:
			target = t.Table
		case *plan.CreateCheck:
			target = t.Table
			allParticipants = true
		case *plan.DropCheck:
			target = t.Table
			allParticipants = true
		default:
			return true
		}
		transform.Inspect(target, func(child sql.Node) bool {
			if result != nil {
				return false
			}
			rt, ok := child.(*plan.ResolvedTable)
			if !ok {
				return true
			}
			table := core.SQLTableToDoltTable(rt.Table)
			if table == nil {
				return true
			}
			name := table.TableName()
			tableID := id.NewTable(name.Schema, name.Name)
			coll, err := core.GetInheritanceCollectionFromContext(ctx, "")
			if err != nil {
				result = err
				return false
			}
			if len(coll.Descendants(ctx, tableID)) > 0 || (allParticipants && len(coll.GetParents(ctx, tableID)) > 0) {
				result = fmt.Errorf("this schema or truncate operation on inherited table %q is not yet supported", name.Name)
			}
			return true
		})
		return false
	})
	return result
}

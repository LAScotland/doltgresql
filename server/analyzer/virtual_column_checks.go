// Copyright 2026 Dolthub, Inc.
// Licensed under the Apache License, Version 2.0

package analyzer

import (
	"fmt"
	"strings"

	"github.com/dolthub/go-mysql-server/sql"
	"github.com/dolthub/go-mysql-server/sql/analyzer"
	gmsexpression "github.com/dolthub/go-mysql-server/sql/expression"
	"github.com/dolthub/go-mysql-server/sql/plan"
	"github.com/dolthub/go-mysql-server/sql/planbuilder"
	"github.com/dolthub/go-mysql-server/sql/transform"
	vitess "github.com/dolthub/vitess/go/vt/sqlparser"

	"github.com/dolthub/doltgresql/core"
)

// restoreVirtualColumnTableChecks repairs a narrow GMS plan-builder capability
// gap: VirtualColumnTable preserves the expression-index projections needed by
// execution, but does not itself expose the underlying table's CheckTable
// interface, so INSERT and UPDATE plans are built without persisted checks.
func restoreVirtualColumnTableChecks(ctx *sql.Context, a *analyzer.Analyzer, n sql.Node, _ *plan.Scope, _ analyzer.RuleSelector, _ *sql.QueryFlags) (sql.Node, transform.TreeIdentity, error) {
	checkNode, ok := n.(sql.CheckConstraintNode)
	if !ok {
		return n, transform.SameTree, nil
	}
	var targets []*plan.ResolvedTable
	var updateTargetSource string
	var updateEvaluationSchema sql.Schema
	switch dml := n.(type) {
	case *plan.InsertInto:
		transform.Inspect(dml.Destination, func(candidate sql.Node) bool {
			if rt, ok := candidate.(*plan.ResolvedTable); ok {
				targets = append(targets, rt)
				return false
			}
			return true
		})
	case *plan.Update:
		var updateTableID sql.TableId
		foundUpdateTarget := false
		transform.Inspect(dml.Child, func(candidate sql.Node) bool {
			expressioner, ok := candidate.(sql.Expressioner)
			if !ok {
				return true
			}
			for _, candidateExpr := range expressioner.Expressions() {
				setField, ok := candidateExpr.(*gmsexpression.SetField)
				if !ok {
					continue
				}
				if field, ok := setField.LeftChild.(*gmsexpression.GetField); ok {
					updateTableID = field.TableId()
					updateTargetSource = field.Table()
					foundUpdateTarget = true
					return false
				}
			}
			return true
		})
		if !foundUpdateTarget {
			return n, transform.SameTree, nil
		}
		childSchema := dml.Child.Schema(ctx)
		if len(childSchema)%2 == 0 {
			updateEvaluationSchema = childSchema[:len(childSchema)/2]
		}
		transform.Inspect(dml.Child, func(candidate sql.Node) bool {
			var rt *plan.ResolvedTable
			switch tableNode := candidate.(type) {
			case *plan.ResolvedTable:
				rt = tableNode
			case *plan.IndexedTableAccess:
				rt, _ = tableNode.TableNode.(*plan.ResolvedTable)
			}
			if rt != nil {
				if rt.Id() == updateTableID {
					targets = append(targets, rt)
				}
				return false
			}
			return true
		})
	default:
		return n, transform.SameTree, nil
	}

	checks := append(sql.CheckConstraints(nil), checkNode.Checks()...)
	existing := make(map[string]struct{}, len(checks))
	for _, check := range checks {
		existing[check.Name] = struct{}{}
	}
	for _, target := range targets {
		virtual, ok := target.Table.(*plan.VirtualColumnTable)
		if !ok {
			continue
		}
		base := virtual.Underlying()
		for {
			wrapper, ok := base.(sql.TableWrapper)
			if !ok {
				break
			}
			base = wrapper.Underlying()
		}
		checkTable, ok := base.(sql.CheckTable)
		if !ok {
			continue
		}
		definitions, err := checkTable.GetChecks(ctx)
		if err != nil {
			return nil, transform.SameTree, err
		}
		physical := core.SQLTableToDoltTable(base)
		if physical == nil {
			continue
		}
		name := physical.TableName()
		from := quoteCheckIdentifier(name.Schema) + "." + quoteCheckIdentifier(name.Name)
		targetSchema := target.Schema(ctx)
		checkSchema := targetSchema
		if len(updateEvaluationSchema) > 0 {
			checkSchema = updateEvaluationSchema
		}
		for _, definition := range definitions {
			if _, ok := existing[definition.Name]; ok {
				continue
			}
			parsed, err := a.Parser.ParseSimple(fmt.Sprintf("SELECT %s FROM %s", definition.CheckExpression, from))
			if err != nil {
				return nil, transform.SameTree, err
			}
			selectStmt, ok := parsed.(*vitess.Select)
			if !ok || len(selectStmt.SelectExprs) != 1 || len(selectStmt.From) != 1 {
				return nil, transform.SameTree, sql.ErrInvalidCheckConstraint.New(definition.CheckExpression)
			}
			aliased, ok := selectStmt.SelectExprs[0].(*vitess.AliasedExpr)
			if !ok {
				return nil, transform.SameTree, sql.ErrInvalidCheckConstraint.New(definition.CheckExpression)
			}
			expr := planbuilder.New(ctx, a.Catalog, nil).BuildScalarWithTable(aliased.Expr, selectStmt.From[0])
			expr, _, err = transform.Expr(ctx, expr, func(_ *sql.Context, candidate sql.Expression) (sql.Expression, transform.TreeIdentity, error) {
				field, ok := candidate.(*gmsexpression.GetField)
				if !ok {
					return candidate, transform.SameTree, nil
				}
				index := -1
				for i, column := range checkSchema {
					if column.Name == field.Name() && (updateTargetSource == "" || strings.EqualFold(column.Source, updateTargetSource)) {
						index = i
						break
					}
				}
				if index < 0 {
					return nil, transform.SameTree, sql.ErrColumnNotFound.New(field.Name())
				}
				return field.WithIndex(index), transform.NewTree, nil
			})
			if err != nil {
				return nil, transform.SameTree, err
			}
			checks = append(checks, &sql.CheckConstraint{Name: definition.Name, Expr: expr, Enforced: definition.Enforced, IsNotValid: definition.IsNotValid})
			existing[definition.Name] = struct{}{}
		}
	}
	if len(checks) == len(checkNode.Checks()) {
		return n, transform.SameTree, nil
	}
	return checkNode.WithChecks(checks), transform.NewTree, nil
}

func quoteCheckIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

// Copyright 2023 Dolthub, Inc.
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

package ast

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/dolthub/go-mysql-server/sql"

	vitess "github.com/dolthub/vitess/go/vt/sqlparser"

	"github.com/dolthub/doltgresql/postgres/parser/sem/tree"
	"github.com/dolthub/doltgresql/server/auth"
	pgtypes "github.com/dolthub/doltgresql/server/types"
)

// nodeCreateTable handles *tree.CreateTable nodes.
func nodeCreateTable(ctx *Context, node *tree.CreateTable) (*vitess.DDL, error) {
	if node == nil {
		return nil, nil
	}
	if len(node.StorageParams) > 0 {
		return nil, errors.Errorf("storage parameters are not yet supported")
	}
	// TODO: support tree.CreateTableOnCommitDrop and tree.CreateTableOnCommitDeleteRows
	switch node.OnCommit {
	case tree.CreateTableOnCommitDrop:
		// is unsupported and ignored
	case tree.CreateTableOnCommitDeleteRows:
		// is unsupported and ignored
	}
	tableName, err := nodeTableName(ctx, &node.Table)
	if err != nil {
		return nil, err
	}
	var isTemporary bool
	switch node.Persistence {
	case tree.PersistencePermanent:
		isTemporary = false
	case tree.PersistenceTemporary:
		isTemporary = true
	case tree.PersistenceUnlogged:
		return nil, errors.Errorf("UNLOGGED is not yet supported")
	default:
		return nil, errors.Errorf("unknown persistence strategy encountered")
	}
	var optSelect *vitess.OptSelect
	if node.Using != "" {
		return nil, errors.Errorf("USING is not yet supported")
	}
	if node.Tablespace != "" {
		return nil, errors.Errorf("TABLESPACE is not yet supported")
	}
	if node.AsSource != nil {
		selectStmt, err := nodeSelect(ctx, node.AsSource)
		if err != nil {
			return nil, err
		}
		optSelect = &vitess.OptSelect{
			Select: selectStmt,
		}
	}
	var optLike *vitess.OptLike
	if len(node.Inherits) > 0 {
		if err := validateInheritedChildConstraints(node.Defs); err != nil {
			return nil, err
		}
		optLike = &vitess.OptLike{
			LikeTables: []vitess.TableName{},
		}
		for _, table := range node.Inherits {
			likeTable, err := nodeTableName(ctx, &table)
			if err != nil {
				return nil, err
			}
			optLike.LikeTables = append(optLike.LikeTables, likeTable)
		}
	}
	if node.WithNoData {
		return nil, errors.Errorf("WITH NO DATA is not yet supported")
	}
	ddl := &vitess.DDL{
		Action:      vitess.CreateStr,
		Table:       tableName,
		IfNotExists: node.IfNotExists,
		Temporary:   isTemporary,
		OptSelect:   optSelect,
		OptLike:     optLike,
		Auth: vitess.AuthInformation{
			AuthType:    auth.AuthType_CREATE,
			TargetType:  auth.AuthTargetType_SchemaIdentifiers,
			TargetNames: []string{tableName.DbQualifier.String(), tableName.SchemaQualifier.String()},
		},
	}
	if err = assignTableDefs(ctx, node.Defs, ddl); err != nil {
		return nil, err
	}
	if len(node.Inherits) > 0 {
		// OptLike is currently the only planner route that can merge parent
		// columns. Add a typed, temporary column so the Doltgres analyzer can
		// distinguish INHERITS from LIKE and restore PostgreSQL key semantics.
		if ddl.TableSpec == nil {
			ddl.TableSpec = &vitess.TableSpec{}
		}
		markerName, err := inheritsMarkerColumnName(ddl.TableSpec)
		if err != nil {
			return nil, err
		}
		childColumns := make([]InheritsColumn, 0, len(ddl.TableSpec.Columns))
		for _, column := range ddl.TableSpec.Columns {
			columnType, ok := column.Type.ResolvedType.(sql.Type)
			if !ok {
				return nil, errors.Errorf("unresolved inherited child column type for %q", column.Name.String())
			}
			childColumns = append(childColumns, InheritsColumn{Name: column.Name.String(), Type: columnType})
		}
		ddl.TableSpec.AddColumn(&vitess.ColumnDefinition{
			Name: vitess.NewColIdent(markerName),
			Type: vitess.ColumnType{
				ResolvedType: pgtypes.Int32,
				Type:         "int4",
				Default: vitess.InjectedExpr{Expression: &InheritsMetadata{
					PrimaryKeyColumns: inheritedTablePrimaryKeyColumns(node.Defs),
					Parents:           inheritanceParentNames(optLike.LikeTables),
					TargetName:        string(node.Table.ObjectName),
					ChildColumns:      childColumns,
				}},
			},
		})
	}

	if node.PartitionBy != nil {
		switch node.PartitionBy.Type {
		case tree.PartitionByList:
			if len(node.PartitionBy.Elems) != 1 {
				return nil, errors.Errorf("PARTITION BY LIST must have a single column or expression")
			}
		}

		// GMS does not support PARTITION BY, so we parse it and ignore it
		if ddl.TableSpec != nil {
			ddl.TableSpec.PartitionOpt = &vitess.PartitionOption{
				PartitionType: string(node.PartitionBy.Type),
				Expr:          vitess.NewColName(string(node.PartitionBy.Elems[0].Column)),
			}
		}
	}
	if node.PartitionOf.Table() != "" {
		return nil, errors.Errorf("PARTITION OF is not yet supported")
	}
	return ddl, nil
}

func inheritedTablePrimaryKeyColumns(defs tree.TableDefs) []string {
	for _, def := range defs {
		if constraint, ok := def.(*tree.UniqueConstraintTableDef); ok && constraint.PrimaryKey {
			columns := make([]string, len(constraint.Columns))
			for i, column := range constraint.Columns {
				columns[i] = string(column.Column)
			}
			return columns
		}
	}
	for _, def := range defs {
		if column, ok := def.(*tree.ColumnTableDef); ok && column.PrimaryKey.IsPrimaryKey {
			return []string{string(column.Name)}
		}
	}
	return nil
}

func inheritsMarkerColumnName(spec *vitess.TableSpec) (string, error) {
	existing := make(map[string]struct{}, len(spec.Columns))
	for _, column := range spec.Columns {
		existing[strings.ToLower(column.Name.String())] = struct{}{}
	}
	for {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", fmt.Errorf("generate INHERITS planning marker: %w", err)
		}
		name := "__doltgres_inherits_" + hex.EncodeToString(random[:])
		if _, found := existing[name]; !found {
			return name, nil
		}
	}
}

func validateInheritedChildConstraints(defs tree.TableDefs) error {
	for _, def := range defs {
		switch def := def.(type) {
		case *tree.UniqueConstraintTableDef:
			if !def.PrimaryKey {
				return errors.Errorf("UNIQUE constraints on an inherited child are not yet supported")
			}
		case *tree.IndexTableDef:
			return errors.Errorf("indexes on an inherited child are not yet supported")
		case *tree.CheckConstraintTableDef:
			return errors.Errorf("CHECK constraints on an inherited child are not yet supported")
		case *tree.ForeignKeyConstraintTableDef:
			return errors.Errorf("foreign keys on an inherited child are not yet supported")
		case *tree.ColumnTableDef:
			if def.Unique && !def.PrimaryKey.IsPrimaryKey {
				return errors.Errorf("UNIQUE constraints on an inherited child are not yet supported")
			}
			if len(def.CheckExprs) > 0 {
				return errors.Errorf("CHECK constraints on an inherited child are not yet supported")
			}
			if def.References.Table != nil {
				return errors.Errorf("foreign keys on an inherited child are not yet supported")
			}
		}
	}
	return nil
}

func inheritanceParentNames(tables []vitess.TableName) []InheritsParent {
	parents := make([]InheritsParent, len(tables))
	for i, t := range tables {
		parents[i] = InheritsParent{Database: t.DbQualifier.String(), Schema: t.SchemaQualifier.String(), Name: t.Name.String()}
	}
	return parents
}

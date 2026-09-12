// Copyright 2026 Dolthub, Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0
package node

import (
	"fmt"
	"github.com/dolthub/go-mysql-server/sql"
	"io"
)

// InheritedTable scans a parent and its descendants with the parent's column layout.
// It deliberately exposes neither indexes nor mutation interfaces: a parent's
// indexes and uniqueness constraints say nothing about its descendants.
type InheritedTable struct {
	parent   sql.Table
	tables   []sql.Table
	ordinals [][]int
	schema   sql.Schema
}

func NewInheritedTable(ctx *sql.Context, parent sql.Table, children []sql.Table) (*InheritedTable, error) {
	t := &InheritedTable{parent: parent, tables: append([]sql.Table{parent}, children...)}
	for _, c := range parent.Schema(ctx) {
		copy := *c
		copy.PrimaryKey = false
		t.schema = append(t.schema, &copy)
	}
	for _, table := range t.tables {
		sch := table.Schema(ctx)
		mapping := make([]int, len(t.schema))
		for j, c := range t.schema {
			found := -1
			for k, childCol := range sch {
				if childCol.Name == c.Name {
					found = k
					if !childCol.Type.Equals(c.Type) {
						return nil, fmt.Errorf("inherited column %q has incompatible type in table %q", c.Name, table.Name())
					}
					break
				}
			}
			if found < 0 {
				return nil, fmt.Errorf("inherited column %q is missing from table %q", c.Name, table.Name())
			}
			mapping[j] = found
		}
		t.ordinals = append(t.ordinals, mapping)
	}
	return t, nil
}
func (t *InheritedTable) Name() string                   { return t.parent.Name() }
func (t *InheritedTable) String() string                 { return t.parent.String() + " (including descendants)" }
func (t *InheritedTable) Schema(*sql.Context) sql.Schema { return t.schema }
func (t *InheritedTable) Collation() sql.CollationID     { return t.parent.Collation() }
func (t *InheritedTable) Partitions(ctx *sql.Context) (sql.PartitionIter, error) {
	return &inheritancePartitionIter{tables: t.tables}, nil
}
func (t *InheritedTable) PartitionRows(ctx *sql.Context, p sql.Partition) (sql.RowIter, error) {
	part, ok := p.(inheritancePartition)
	if !ok {
		return nil, fmt.Errorf("unexpected inheritance partition %T", p)
	}
	iter, err := t.tables[part.table].PartitionRows(ctx, part.partition)
	if err != nil {
		return nil, err
	}
	return &inheritanceRows{RowIter: iter, ordinals: t.ordinals[part.table]}, nil
}

type inheritancePartition struct {
	table     int
	partition sql.Partition
}

func (p inheritancePartition) Key() []byte {
	return append([]byte(fmt.Sprintf("%d:", p.table)), p.partition.Key()...)
}

type inheritancePartitionIter struct {
	tables  []sql.Table
	index   int
	current sql.PartitionIter
}

func (i *inheritancePartitionIter) Next(ctx *sql.Context) (sql.Partition, error) {
	for i.index < len(i.tables) {
		if i.current == nil {
			var err error
			i.current, err = i.tables[i.index].Partitions(ctx)
			if err != nil {
				return nil, err
			}
		}
		p, err := i.current.Next(ctx)
		if err == io.EOF {
			if e := i.current.Close(ctx); e != nil {
				return nil, e
			}
			i.current = nil
			i.index++
			continue
		}
		if err != nil {
			return nil, err
		}
		return inheritancePartition{i.index, p}, nil
	}
	return nil, io.EOF
}
func (i *inheritancePartitionIter) Close(ctx *sql.Context) error {
	if i.current != nil {
		return i.current.Close(ctx)
	}
	return nil
}

type inheritanceRows struct {
	sql.RowIter
	ordinals []int
}

func (i *inheritanceRows) Next(ctx *sql.Context) (sql.Row, error) {
	row, err := i.RowIter.Next(ctx)
	if err != nil {
		return nil, err
	}
	out := make(sql.Row, len(i.ordinals))
	for j, k := range i.ordinals {
		out[j] = row[k]
	}
	return out, nil
}

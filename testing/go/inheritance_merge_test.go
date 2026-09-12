// Copyright 2026 Dolthub, Inc.
// Licensed under the Apache License, Version 2.0 (the "License").
package _go

import (
	"github.com/dolthub/go-mysql-server/sql"
	"testing"
)

func TestInheritanceMerge(t *testing.T) {
	RunScripts(t, []ScriptTest{{
		Name: "merge retains inherited rows and relationships from independent branches",
		SetUpScript: []string{
			`CREATE TABLE merge_parent (id INT PRIMARY KEY)`,
			`INSERT INTO merge_parent VALUES (1)`,
			`SELECT DOLT_COMMIT('-Am', 'parent baseline')`,
			`SELECT DOLT_CHECKOUT('-b', 'child_change')`,
			`CREATE TABLE merge_child (PRIMARY KEY (id)) INHERITS (merge_parent)`,
			`INSERT INTO merge_child VALUES (2)`,
			`SELECT DOLT_COMMIT('-Am', 'child change')`,
			`SELECT DOLT_CHECKOUT('main')`,
			`INSERT INTO merge_parent VALUES (3)`,
			`SELECT DOLT_COMMIT('-Am', 'independent parent row')`,
		},
		Assertions: []ScriptTestAssertion{
			{Query: `SELECT DOLT_MERGE('child_change')`, SkipResultsCheck: true},
			{Query: `SELECT id FROM merge_parent ORDER BY id`, Expected: []sql.Row{{1}, {2}, {3}}},
			{Query: `SELECT id FROM ONLY merge_parent ORDER BY id`, Expected: []sql.Row{{1}, {3}}},
			{Query: `SELECT count(*) FROM pg_inherits WHERE inhrelid = 'merge_child'::regclass AND inhparent = 'merge_parent'::regclass`, Expected: []sql.Row{{int64(1)}}},
		},
	}})
}

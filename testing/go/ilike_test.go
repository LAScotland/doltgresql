// Copyright 2026 Dolthub, Inc.
// Licensed under the Apache License, Version 2.0 (the "License");

package _go

import (
	"testing"

	"github.com/dolthub/go-mysql-server/sql"
)

func TestILike(t *testing.T) {
	RunScripts(t, []ScriptTest{{
		Name: "case insensitive pattern matching",
		SetUpScript: []string{
			`CREATE TABLE ilike_partner (id integer PRIMARY KEY, name text)`,
			`INSERT INTO ilike_partner VALUES (1, 'Mitchell Admin'), (2, 'ACME'), (3, 'ångström'), (4, 'a_b'), (5, 'a%b'), (6, NULL)`,
		},
		Assertions: []ScriptTestAssertion{
			{Query: `SELECT id FROM ilike_partner WHERE name ILIKE '%MIT%' ORDER BY id`, Expected: []sql.Row{{1}}},
			{Query: `SELECT id FROM ilike_partner WHERE name NOT ILIKE 'a%' ORDER BY id`, Expected: []sql.Row{{1}, {3}}},
			{Query: `SELECT 'ångström' ILIKE 'ÅNG%'`, Expected: []sql.Row{{"t"}}},
			{Query: `SELECT 'Straße' ILIKE 'STRASSE'`, Expected: []sql.Row{{"f"}}},
			{Query: `SELECT 'a_b' ILIKE 'A!_B' ESCAPE '!'`, Expected: []sql.Row{{"t"}}},
			{Query: `SELECT 'a%b' ILIKE 'A!%B' ESCAPE '!'`, Expected: []sql.Row{{"t"}}},
			{Query: `SELECT 'A' ILIKE 'A' ESCAPE 'a'`, Expected: []sql.Row{{"t"}}},
			{Query: `SELECT '%' ILIKE 'A%' ESCAPE 'A'`, Expected: []sql.Row{{"t"}}},
			{Query: `SELECT 'axb' NOT ILIKE 'A!_B' ESCAPE '!'`, Expected: []sql.Row{{"t"}}},
			{Query: `SELECT NULL::text ILIKE '%'`, Expected: []sql.Row{{nil}}},
			{Query: `SELECT 'anything' NOT ILIKE NULL::text`, Expected: []sql.Row{{nil}}},
			{Query: `SELECT 'anything' ILIKE '%' ESCAPE NULL::text`, Expected: []sql.Row{{nil}}},
			{Query: `SELECT 'anything' ILIKE '%' ESCAPE '!!'`, ExpectedErr: "Invalid argument"},
			{Query: `SELECT 'Mitchell' LIKE 'MIT%'`, Expected: []sql.Row{{"f"}}},
		},
	}})
}

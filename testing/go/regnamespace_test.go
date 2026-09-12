// Copyright 2026 Dolthub, Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
package _go

import (
	"github.com/dolthub/go-mysql-server/sql"
	"testing"
)

func TestRegnamespace(t *testing.T) {
	RunScripts(t, []ScriptTest{{
		Name: "schema OID aliases",
		SetUpScript: []string{
			`CREATE SCHEMA "Mixed Case";`,
			`CREATE SCHEMA "a.b";`,
			`CREATE SCHEMA "has""quote";`,
			`CREATE SCHEMA "char";`,
			`CREATE TABLE namespaces (id int PRIMARY KEY, n regnamespace, ns regnamespace[]);`,
			`INSERT INTO namespaces VALUES (1, 'public', ARRAY['public'::regnamespace, 'pg_catalog'::regnamespace]);`,
		},
		Assertions: []ScriptTestAssertion{
			{Query: `SELECT current_schema::regnamespace::text;`, Expected: []sql.Row{{"public"}}},
			{Query: `SELECT nspname FROM pg_namespace WHERE oid = current_schema::regnamespace;`, Expected: []sql.Row{{"public"}}},
			{Query: `SELECT count(*) FROM pg_proc p WHERE p.proname = 'unaccent' AND p.pronamespace = current_schema::regnamespace AND p.pronargs = 1;`, Expected: []sql.Row{{0}}},
			{Query: `SELECT 'PUBLIC'::regnamespace::text, '"Mixed Case"'::regnamespace::text, '"a.b"'::regnamespace::text, '"has""quote"'::regnamespace::text, '"char"'::regnamespace::oid = to_regnamespace('char')::oid;`, Expected: []sql.Row{{"public", `"Mixed Case"`, `"a.b"`, `"has""quote"`, "t"}}},
			{Query: `SELECT 'pg_catalog'::regnamespace::oid, 11::regnamespace::text, 11::oid::regnamespace::text;`, Expected: []sql.Row{{11, "pg_catalog", "pg_catalog"}}},
			{Query: `SELECT '0'::regnamespace::text, '-'::regnamespace::oid, '4294967295'::regnamespace::text;`, Expected: []sql.Row{{"-", 0, "4294967295"}}},
			{Query: `SELECT NULL::regnamespace IS NULL, to_regnamespace(NULL) IS NULL, to_regnamespace('missing_schema') IS NULL, to_regnamespace('a.b') IS NULL, to_regnamespace('4294967296') IS NULL;`, Expected: []sql.Row{{"t", "t", "t", "t", "t"}}},
			{Query: `SELECT to_regnamespace('11')::text, to_regnamespace('-')::text;`, Expected: []sql.Row{{"pg_catalog", "-"}}},
			{Query: `SELECT n::text, ns::text FROM namespaces;`, Expected: []sql.Row{{"public", "{public,pg_catalog}"}}},
			{Query: `SELECT 'pg_catalog'::regnamespace::int4, 'pg_catalog'::regnamespace::int8, 11::int2::regnamespace::text, 11::int8::regnamespace::text;`, Expected: []sql.Row{{11, 11, "pg_catalog", "pg_catalog"}}},
			{Query: `SELECT encode(regnamespacesend('pg_catalog'::regnamespace), 'hex');`, Expected: []sql.Row{{"0000000b"}}},
			{Query: `SELECT 'missing_schema'::regnamespace;`, ExpectedErr: `schema "missing_schema" does not exist`},
			{Query: `SELECT 'a.b'::regnamespace;`, ExpectedErr: "invalid name syntax"},
			{Query: `SELECT '"unterminated'::regnamespace;`, ExpectedErr: "invalid name syntax"},
			{Query: `SELECT '4294967296'::regnamespace;`, ExpectedErr: "out of range"},
			{Query: `SET search_path TO "Mixed Case";`},
			{Query: `SELECT quote_ident(current_schema)::regnamespace::text, 'public'::regnamespace::text;`, Expected: []sql.Row{{`"Mixed Case"`, "public"}}},
		},
	}})
}

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

package _go

import (
	"testing"

	"github.com/dolthub/go-mysql-server/sql"
)

func TestDropViewCascade(t *testing.T) {
	RunScripts(t, []ScriptTest{
		{
			Name: "drops a view without dependents",
			SetUpScript: []string{
				`CREATE TABLE dvc_base (id INT PRIMARY KEY);`,
				`CREATE VIEW res_device AS SELECT id FROM dvc_base;`,
			},
			Assertions: []ScriptTestAssertion{
				{Query: `DROP VIEW res_device CASCADE;`, Expected: []sql.Row{}},
				{Query: `SELECT * FROM res_device;`, ExpectedErr: `table not found: res_device`},
			},
		},
		{
			Name:        "missing and wrong relation type",
			SetUpScript: []string{`CREATE TABLE dvc_table (id INT);`},
			Assertions: []ScriptTestAssertion{
				{Query: `DROP VIEW IF EXISTS dvc_missing CASCADE;`, Expected: []sql.Row{}},
				{Query: `DROP VIEW IF EXISTS dvc_missing_one, dvc_missing_two CASCADE;`, Expected: []sql.Row{}},
				{Query: `DROP VIEW dvc_missing CASCADE;`, ExpectedErr: `view "dvc_missing" does not exist`},
				{Query: `DROP VIEW dvc_table CASCADE;`, ExpectedErr: `"dvc_table" is not a view`},
				{Query: `SELECT COUNT(*) FROM dvc_table;`, Expected: []sql.Row{{int64(0)}}},
			},
		},
		{
			Name: "duplicate requested view is dropped once",
			SetUpScript: []string{
				`CREATE TABLE dvc_duplicate_base (id INT);`,
				`CREATE VIEW dvc_duplicate AS SELECT id FROM dvc_duplicate_base;`,
			},
			Assertions: []ScriptTestAssertion{
				{Query: `DROP VIEW dvc_duplicate, dvc_duplicate CASCADE;`, Expected: []sql.Row{}},
			},
		},
		{
			Name: "schema qualified and search path",
			SetUpScript: []string{
				`CREATE SCHEMA dvc_schema;`,
				`CREATE TABLE dvc_schema.base (id INT);`,
				`CREATE VIEW dvc_schema."DeviceView" AS SELECT id FROM dvc_schema.base;`,
				`SET search_path TO dvc_schema, public;`,
			},
			Assertions: []ScriptTestAssertion{
				{Query: `DROP VIEW "DeviceView" CASCADE;`, Expected: []sql.Row{}},
				{Query: `SELECT * FROM dvc_schema."DeviceView";`, ExpectedErr: `table not found: deviceview`},
			},
		},
		{
			Name: "unqualified name does not cross current schema authorization binding",
			SetUpScript: []string{
				`CREATE SCHEMA dvc_empty;`,
				`CREATE TABLE dvc_auth_base (id INT);`,
				`CREATE VIEW dvc_auth_view AS SELECT id FROM dvc_auth_base;`,
				`SET search_path TO dvc_empty, public;`,
			},
			Assertions: []ScriptTestAssertion{
				{Query: `DROP VIEW dvc_auth_view CASCADE;`, ExpectedErr: `view "dvc_auth_view" does not exist`},
				{Query: `SELECT COUNT(*) FROM public.dvc_auth_view;`, Expected: []sql.Row{{int64(0)}}},
				{Query: `DROP VIEW public.dvc_auth_view CASCADE;`, Expected: []sql.Row{}},
			},
		},
		{
			Name: "dependent views fail closed and leave all views",
			SetUpScript: []string{
				`CREATE TABLE dvc_dep_base (id INT);`,
				`CREATE VIEW dvc_parent AS SELECT id FROM dvc_dep_base;`,
				`CREATE VIEW dvc_child AS SELECT id FROM dvc_parent;`,
			},
			Assertions: []ScriptTestAssertion{
				{Query: `DROP VIEW dvc_parent CASCADE;`, ExpectedErr: `DROP VIEW CASCADE with dependent views is not yet supported`},
				{Query: `SELECT COUNT(*) FROM dvc_parent;`, Expected: []sql.Row{{int64(0)}}},
				{Query: `SELECT COUNT(*) FROM dvc_child;`, Expected: []sql.Row{{int64(0)}}},
			},
		},
		{
			Name: "requested dependent views are preflighted as one set",
			SetUpScript: []string{
				`CREATE TABLE dvc_multi_base (id INT);`,
				`CREATE VIEW dvc_multi_parent AS SELECT id FROM dvc_multi_base;`,
				`CREATE VIEW dvc_multi_child AS SELECT id FROM dvc_multi_parent;`,
			},
			Assertions: []ScriptTestAssertion{
				{Query: `DROP VIEW dvc_multi_parent, dvc_multi_child CASCADE;`, Expected: []sql.Row{}},
				{Query: `SELECT * FROM dvc_multi_parent;`, ExpectedErr: `table not found: dvc_multi_parent`},
				{Query: `SELECT * FROM dvc_multi_child;`, ExpectedErr: `table not found: dvc_multi_child`},
			},
		},
		{
			Name: "transaction rollback restores view",
			SetUpScript: []string{
				`CREATE TABLE dvc_rollback_base (id INT);`,
				`CREATE VIEW dvc_rollback AS SELECT id FROM dvc_rollback_base;`,
				`BEGIN;`,
				`DROP VIEW dvc_rollback CASCADE;`,
				`ROLLBACK;`,
			},
			Assertions: []ScriptTestAssertion{
				{Query: `SELECT COUNT(*) FROM dvc_rollback;`, Expected: []sql.Row{{int64(0)}}},
			},
		},
	})
}

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

func TestTupleUpdateAssignment(t *testing.T) {
	RunScripts(t, []ScriptTest{
		{
			Name: "ordinary update assigns tuple positions simultaneously",
			SetUpScript: []string{
				`CREATE TABLE tuple_update (id INT PRIMARY KEY, a INT, b INT);`,
				`INSERT INTO tuple_update VALUES (1, 10, 20);`,
			},
			Assertions: []ScriptTestAssertion{
				{Query: `UPDATE tuple_update SET (a, b) = (b, a) WHERE id = 1;`, Expected: []sql.Row{}},
				{Query: `SELECT a, b FROM tuple_update WHERE id = 1;`, Expected: []sql.Row{{int32(20), int32(10)}}},
			},
		},
		{
			Name: "on conflict tuple assignment preserves expression types and positions",
			SetUpScript: []string{
				`CREATE TABLE tuple_upsert(id serial PRIMARY KEY, model text UNIQUE, name jsonb, info text);`,
				`INSERT INTO tuple_upsert(model,name,info) VALUES ('x','{"en_US":"Old"}','old');`,
			},
			Assertions: []ScriptTestAssertion{
				{
					Query:    `INSERT INTO tuple_upsert(model,name,info) VALUES ('x','{"en_US":"New"}','new'),('y','{"en_US":"Second"}','second') ON CONFLICT(model) DO UPDATE SET (model,name,info) = (EXCLUDED.model, CASE WHEN tuple_upsert.name ->> 'en_US' IS DISTINCT FROM EXCLUDED.name ->> 'en_US' THEN EXCLUDED.name ELSE COALESCE(tuple_upsert.name,'{}'::jsonb) || EXCLUDED.name END, EXCLUDED.info);`,
					Expected: []sql.Row{},
				},
				{Query: `SELECT model, name::text, info FROM tuple_upsert ORDER BY model;`, Expected: []sql.Row{{"x", `{"en_US": "New"}`, "new"}, {"y", `{"en_US": "Second"}`, "second"}}},
			},
		},
		{
			Name: "arity mismatch fails without mutation",
			SetUpScript: []string{
				`CREATE TABLE tuple_arity (id INT PRIMARY KEY, a INT, b INT);`,
				`INSERT INTO tuple_arity VALUES (1, 10, 20);`,
			},
			Assertions: []ScriptTestAssertion{
				{Query: `UPDATE tuple_arity SET (a, b) = (1) WHERE id = 1;`, ExpectedErr: `number of columns does not match number of values`},
				{Query: `UPDATE tuple_arity SET (a, b) = (1, 2, 3) WHERE id = 1;`, ExpectedErr: `number of columns does not match number of values`},
				{Query: `SELECT a, b FROM tuple_arity;`, Expected: []sql.Row{{int32(10), int32(20)}}},
			},
		},
		{
			Name: "tuple subquery fails closed",
			SetUpScript: []string{
				`CREATE TABLE tuple_subquery (id INT PRIMARY KEY, a INT, b INT);`,
				`INSERT INTO tuple_subquery VALUES (1, 10, 20);`,
			},
			Assertions: []ScriptTestAssertion{
				{Query: `UPDATE tuple_subquery SET (a, b) = (SELECT b, a FROM tuple_subquery WHERE id = 1) WHERE id = 1;`, ExpectedErr: `tuple assignment from a subquery is not yet supported`},
				{Query: `SELECT a, b FROM tuple_subquery;`, Expected: []sql.Row{{int32(10), int32(20)}}},
			},
		},
	})
}

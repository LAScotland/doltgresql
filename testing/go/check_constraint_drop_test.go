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

func TestDropCheckConstraintCompatibility(t *testing.T) {
	RunScripts(t, []ScriptTest{
		{
			Name: "drop check from table with self-referencing foreign keys",
			SetUpScript: []string{
				`CREATE TABLE check_drop_partner(id integer PRIMARY KEY, parent_id integer, commercial_partner_id integer, name text, type text,
					CONSTRAINT check_drop_partner_parent_fk FOREIGN KEY(parent_id) REFERENCES check_drop_partner(id),
					CONSTRAINT check_drop_partner_commercial_fk FOREIGN KEY(commercial_partner_id) REFERENCES check_drop_partner(id),
					CONSTRAINT check_drop_partner_name CHECK ((type = 'contact' AND name IS NOT NULL) OR type != 'contact'))`,
				`CREATE INDEX check_drop_partner_lower_name_idx ON check_drop_partner ((lower(name)))`,
			},
			Assertions: []ScriptTestAssertion{
				{Query: `ALTER TABLE check_drop_partner DROP CONSTRAINT missing_check`, ExpectedErr: "does not exist"},
				{Query: `ALTER TABLE check_drop_partner DROP CONSTRAINT IF EXISTS missing_check`, Expected: []sql.Row{}},
				{Query: `ALTER TABLE check_drop_partner DROP CONSTRAINT check_drop_partner_name`, Expected: []sql.Row{}},
				{Query: `SELECT indexname FROM pg_indexes WHERE tablename = 'check_drop_partner' AND indexname = 'check_drop_partner_lower_name_idx'`, Expected: []sql.Row{{"check_drop_partner_lower_name_idx"}}},
				{Query: `INSERT INTO check_drop_partner(id, name, type) VALUES (1, NULL, 'contact')`, Expected: []sql.Row{}},
			},
		},
		{
			Name: "schema-qualified drop check is transactional",
			SetUpScript: []string{
				`CREATE SCHEMA check_drop_schema`,
				`CREATE TABLE check_drop_schema.target(id integer PRIMARY KEY, value integer CONSTRAINT positive_value CHECK (value > 0))`,
				`CREATE INDEX target_expression_idx ON check_drop_schema.target ((value > 0))`,
			},
			Assertions: []ScriptTestAssertion{
				{Query: `BEGIN`, Expected: []sql.Row{}},
				{Query: `ALTER TABLE check_drop_schema.target DROP CONSTRAINT positive_value`, Expected: []sql.Row{}},
				{Query: `ROLLBACK`, Expected: []sql.Row{}},
				{Query: `SELECT conname FROM pg_constraint WHERE conrelid = 'check_drop_schema.target'::regclass AND contype = 'c'`, Expected: []sql.Row{{"positive_value"}}},
				{Query: `ALTER TABLE check_drop_schema.target DROP CONSTRAINT positive_value`, Expected: []sql.Row{}},
				{Query: `INSERT INTO check_drop_schema.target VALUES (1, -1)`, Expected: []sql.Row{}},
				{Query: `ALTER TABLE check_drop_schema.target DROP CONSTRAINT IF EXISTS missing_check`, Expected: []sql.Row{}},
			},
		},
	})
}

// Copyright 2026 Dolthub, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package _go

import (
	"github.com/dolthub/go-mysql-server/sql"
	"testing"
)

func TestDropUniqueConstraintBackingChildForeignKey(t *testing.T) {
	RunScripts(t, []ScriptTest{
		{
			Name: "drop child unique constraint used by foreign key",
			SetUpScript: []string{
				`CREATE TABLE fk_drop_parent(id integer PRIMARY KEY)`,
				`INSERT INTO fk_drop_parent VALUES (1), (2)`,
				`CREATE TABLE fk_drop_child(id integer PRIMARY KEY, user_id integer, CONSTRAINT fk_drop_child_user_key UNIQUE(user_id), CONSTRAINT fk_drop_child_user_fkey FOREIGN KEY(user_id) REFERENCES fk_drop_parent(id))`,
				`INSERT INTO fk_drop_child VALUES (1, 1)`,
			},
			Assertions: []ScriptTestAssertion{
				{Query: `ALTER TABLE fk_drop_child DROP CONSTRAINT fk_drop_child_user_key`, Expected: []sql.Row{}},
				{Query: `INSERT INTO fk_drop_child VALUES (2, 1)`, Expected: []sql.Row{}},
				{Query: `INSERT INTO fk_drop_child VALUES (3, 999)`, ExpectedErr: "violat"},
			},
		},
		{
			Name: "rollback restores child uniqueness and parent unique remains required",
			SetUpScript: []string{
				`CREATE TABLE fk_drop_parent_rollback(id integer PRIMARY KEY, code integer, CONSTRAINT parent_code_key UNIQUE(code))`,
				`INSERT INTO fk_drop_parent_rollback VALUES (1, 10)`,
				`CREATE TABLE fk_drop_child_rollback(id integer PRIMARY KEY, user_id integer, CONSTRAINT child_user_key UNIQUE(user_id), CONSTRAINT child_user_fkey FOREIGN KEY(user_id) REFERENCES fk_drop_parent_rollback(id))`,
				`INSERT INTO fk_drop_child_rollback VALUES (1, 1)`,
				`CREATE TABLE fk_drop_ref(code integer REFERENCES fk_drop_parent_rollback(code))`,
			},
			Assertions: []ScriptTestAssertion{
				{Query: `BEGIN`, Expected: []sql.Row{}},
				{Query: `ALTER TABLE fk_drop_child_rollback DROP CONSTRAINT child_user_key`, Expected: []sql.Row{}},
				{Query: `ROLLBACK`, Expected: []sql.Row{}},
				{Query: `INSERT INTO fk_drop_child_rollback VALUES (2, 1)`, ExpectedErr: "duplicate"},
				{Query: `ALTER TABLE fk_drop_parent_rollback DROP CONSTRAINT parent_code_key`, ExpectedErr: "foreign key"},
			},
		},
		{
			Name: "mixed declaring and referenced dependency fails without adding an index",
			SetUpScript: []string{
				`CREATE TABLE fk_drop_mixed_parent(id integer PRIMARY KEY)`,
				`CREATE TABLE fk_drop_mixed(id integer PRIMARY KEY, link integer, CONSTRAINT mixed_link_key UNIQUE(link), CONSTRAINT mixed_link_parent_fkey FOREIGN KEY(link) REFERENCES fk_drop_mixed_parent(id))`,
				`CREATE TABLE fk_drop_mixed_child(link integer REFERENCES fk_drop_mixed(link))`,
			},
			Assertions: []ScriptTestAssertion{
				{Query: `ALTER TABLE fk_drop_mixed DROP CONSTRAINT mixed_link_key`, ExpectedErr: "foreign key"},
				{Query: `SELECT indexname FROM pg_indexes WHERE tablename = 'fk_drop_mixed' ORDER BY indexname`, Expected: []sql.Row{{"fk_drop_mixed_pkey"}, {"mixed_link_key"}}},
			},
		},
		{
			Name: "composite foreign key replacement index avoids name collisions",
			SetUpScript: []string{
				`CREATE TABLE fk_drop_composite_parent(a integer, b integer, PRIMARY KEY(a, b))`,
				`INSERT INTO fk_drop_composite_parent VALUES (1, 2)`,
				`CREATE TABLE fk_drop_composite_child(id integer PRIMARY KEY, a integer, b integer, CONSTRAINT child_pair_key UNIQUE(a, b), CONSTRAINT child_pair_fk FOREIGN KEY(a, b) REFERENCES fk_drop_composite_parent(a, b))`,
				`CREATE INDEX child_pair_fk ON fk_drop_composite_child(id)`,
				`INSERT INTO fk_drop_composite_child VALUES (1, 1, 2)`,
			},
			Assertions: []ScriptTestAssertion{
				{Query: `ALTER TABLE fk_drop_composite_child DROP CONSTRAINT child_pair_key`, Expected: []sql.Row{}},
				{Query: `INSERT INTO fk_drop_composite_child VALUES (2, 1, 2)`, Expected: []sql.Row{}},
				{Query: `INSERT INTO fk_drop_composite_child VALUES (3, 9, 9)`, ExpectedErr: "violat"},
				{Query: `SELECT indexname FROM pg_indexes WHERE tablename = 'fk_drop_composite_child' ORDER BY indexname`, Expected: []sql.Row{{"child_pair_fk"}, {"child_pair_fk_1"}, {"fk_drop_composite_child_pkey"}}},
			},
		},
		{
			Name: "existing child index is reused without creating another",
			SetUpScript: []string{
				`CREATE TABLE fk_drop_alt_parent(id integer PRIMARY KEY)`,
				`CREATE TABLE fk_drop_alt_child(id integer PRIMARY KEY, parent_id integer, CONSTRAINT alt_parent_key UNIQUE(parent_id), CONSTRAINT alt_parent_fkey FOREIGN KEY(parent_id) REFERENCES fk_drop_alt_parent(id))`,
				`CREATE INDEX explicit_alternative ON fk_drop_alt_child(parent_id)`,
			},
			Assertions: []ScriptTestAssertion{
				{Query: `ALTER TABLE fk_drop_alt_child DROP CONSTRAINT alt_parent_key`, Expected: []sql.Row{}},
				{Query: `SELECT indexname FROM pg_indexes WHERE tablename = 'fk_drop_alt_child' ORDER BY indexname`, Expected: []sql.Row{{"explicit_alternative"}, {"fk_drop_alt_child_pkey"}}},
			},
		},
	})
}

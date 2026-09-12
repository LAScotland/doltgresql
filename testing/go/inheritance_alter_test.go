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

func TestInheritanceAlterTable(t *testing.T) {
	RunScripts(t, []ScriptTest{
		{
			Name: "nullable supported columns propagate serially to all descendants",
			SetUpScript: []string{
				`CREATE TABLE inherit_alter_parent (id INT)`,
				`CREATE TABLE inherit_alter_child (child_value TEXT) INHERITS (inherit_alter_parent)`,
				`CREATE TABLE inherit_alter_grandchild (grandchild_value INT) INHERITS (inherit_alter_child)`,
				`ALTER TABLE inherit_alter_parent ADD COLUMN added_int INT`,
				`ALTER TABLE inherit_alter_parent ADD COLUMN added_text TEXT`,
				`ALTER TABLE inherit_alter_parent ADD COLUMN added_varchar VARCHAR(40)`,
				`ALTER TABLE inherit_alter_parent ADD COLUMN added_json JSONB`,
				`ALTER TABLE inherit_alter_parent ADD COLUMN added_at TIMESTAMP`,
			},
			Assertions: []ScriptTestAssertion{
				{
					Query: `SELECT column_name FROM information_schema.columns
WHERE table_name = 'inherit_alter_grandchild' ORDER BY ordinal_position`,
					Expected: []sql.Row{{"id"}, {"child_value"}, {"grandchild_value"}, {"added_int"}, {"added_text"}, {"added_varchar"}, {"added_json"}, {"added_at"}},
				},
				{
					Query:    `SELECT character_maximum_length FROM information_schema.columns WHERE table_name = 'inherit_alter_grandchild' AND column_name = 'added_varchar'`,
					Expected: []sql.Row{{int64(40)}},
				},
				{Query: `INSERT INTO inherit_alter_grandchild VALUES (1, 'child', 2, 3, 'text', 'varchar', '{"ok":true}', '2026-09-12 12:00:00')`},
			},
		},
		{
			Name: "propagation preflight prevents partial parent alteration on descendant collision",
			SetUpScript: []string{
				`CREATE TABLE inherit_collision_parent (id INT)`,
				`CREATE TABLE inherit_collision_child (later TEXT) INHERITS (inherit_collision_parent)`,
			},
			Assertions: []ScriptTestAssertion{
				{Query: `ALTER TABLE inherit_collision_parent ADD COLUMN later TEXT`, ExpectedErr: `cannot propagate inherited column "later"`},
				{
					Query:    `SELECT count(*) FROM information_schema.columns WHERE table_name = 'inherit_collision_parent' AND column_name = 'later'`,
					Expected: []sql.Row{{int64(0)}},
				},
			},
		},
		{
			Name: "post hook failure rolls back parent and descendant alterations",
			SetUpScript: []string{
				`CREATE TABLE inherit_atomic_parent (id INT)`,
				`CREATE TABLE inherit_atomic_child () INHERITS (inherit_atomic_parent)`,
				`INSERT INTO inherit_atomic_parent VALUES (1)`,
				`CREATE TABLE inherit_atomic_holder (value inherit_atomic_parent)`,
				`INSERT INTO inherit_atomic_holder VALUES (ROW(1)::inherit_atomic_parent)`,
				`CREATE FUNCTION inherit_atomic_reject_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'reject row type update';
END
$$`,
				`CREATE TRIGGER inherit_atomic_reject BEFORE UPDATE ON inherit_atomic_holder
FOR EACH ROW EXECUTE FUNCTION inherit_atomic_reject_update()`,
			},
			Assertions: []ScriptTestAssertion{
				{Query: `ALTER TABLE inherit_atomic_parent ADD COLUMN added INT`, ExpectedErr: "reject row type update"},
				{
					Query: `SELECT table_name, count(*) FROM information_schema.columns
WHERE table_name IN ('inherit_atomic_parent', 'inherit_atomic_child') AND column_name = 'added'
GROUP BY table_name ORDER BY table_name`,
					Expected: []sql.Row{},
				},
				{Query: `BEGIN`},
				{Query: `INSERT INTO inherit_atomic_parent VALUES (2)`},
				{Query: `SAVEPOINT before_inherited_alter`},
				{Query: `ALTER TABLE inherit_atomic_parent ADD COLUMN added INT`, ExpectedErr: "reject row type update"},
				{Query: `ROLLBACK TO SAVEPOINT before_inherited_alter`},
				{Query: `SELECT id FROM ONLY inherit_atomic_parent ORDER BY id`, Expected: []sql.Row{{int32(1)}, {int32(2)}}},
				{Query: `ROLLBACK`},
				{Query: `SELECT id FROM ONLY inherit_atomic_parent ORDER BY id`, Expected: []sql.Row{{int32(1)}}},
			},
		},
		{
			Name: "leaf child may alter its own non-inherited column",
			SetUpScript: []string{
				`CREATE TABLE inherit_own_parent (id INT)`,
				`CREATE TABLE inherit_own_child (own_value INT) INHERITS (inherit_own_parent)`,
				`ALTER TABLE inherit_own_child RENAME COLUMN own_value TO renamed_value`,
				`ALTER TABLE inherit_own_child ALTER COLUMN renamed_value TYPE TEXT`,
				`ALTER TABLE inherit_own_child DROP COLUMN renamed_value`,
			},
			Assertions: []ScriptTestAssertion{
				{Query: `SELECT column_name FROM information_schema.columns WHERE table_name = 'inherit_own_child'`, Expected: []sql.Row{{"id"}}},
			},
		},
		{
			Name: "unsafe inherited graph alterations fail before mutation",
			SetUpScript: []string{
				`CREATE TABLE inherit_guard_parent (id INT)`,
				`CREATE TABLE inherit_guard_child () INHERITS (inherit_guard_parent)`,
				`CREATE SCHEMA inherit_guard_schema`,
				`CREATE TABLE inherit_guard_schema.parent (id INT)`,
				`CREATE TABLE inherit_guard_schema.child () INHERITS (inherit_guard_schema.parent)`,
				`CREATE TABLE inherit_required_parent (id INT NOT NULL)`,
				`CREATE TABLE inherit_required_child () INHERITS (inherit_required_parent)`,
			},
			Assertions: []ScriptTestAssertion{
				{Query: `ALTER TABLE inherit_guard_parent ADD COLUMN required INT NOT NULL`, ExpectedErr: "only supported for nullable columns"},
				{Query: `ALTER TABLE inherit_guard_child DROP COLUMN id`, ExpectedErr: `cannot drop inherited column "id"`},
				{Query: `ALTER TABLE inherit_guard_child RENAME COLUMN id TO changed_id`, ExpectedErr: `cannot rename inherited column "id"`},
				{Query: `ALTER TABLE inherit_guard_child ALTER COLUMN id TYPE TEXT`, ExpectedErr: `cannot alter the type of inherited column "id"`},
				{Query: `ALTER TABLE inherit_guard_child ALTER COLUMN id TYPE TEXT USING id::text`, ExpectedErr: `cannot alter the type of inherited column "id"`},
				{Query: `ALTER TABLE inherit_guard_parent ALTER COLUMN id SET NOT NULL`, ExpectedErr: `cannot alter the nullability of column "id"`},
				{Query: `ALTER TABLE inherit_guard_child ALTER COLUMN id SET NOT NULL`, ExpectedErr: `cannot alter the nullability of inherited column "id"`},
				{Query: `ALTER TABLE inherit_required_parent ALTER COLUMN id DROP NOT NULL`, ExpectedErr: `cannot alter the nullability of column "id"`},
				{Query: `ALTER TABLE inherit_required_child ALTER COLUMN id DROP NOT NULL`, ExpectedErr: `cannot alter the nullability of inherited column "id"`},
				{Query: `ALTER TABLE inherit_guard_parent RENAME TO inherit_guard_renamed`, ExpectedErr: "participates in table inheritance"},
				{Query: `DROP TABLE inherit_guard_child`, ExpectedErr: "participates in table inheritance"},
				{Query: `DROP SCHEMA inherit_guard_schema CASCADE`, ExpectedErr: "DROP SCHEMA with CASCADE behavior is not yet supported"},
				{Query: `SELECT column_name FROM information_schema.columns WHERE table_name = 'inherit_guard_parent'`, Expected: []sql.Row{{"id"}}},
				{Query: `SELECT table_name, is_nullable FROM information_schema.columns WHERE table_name IN ('inherit_guard_parent', 'inherit_guard_child', 'inherit_required_parent', 'inherit_required_child') AND column_name = 'id' ORDER BY table_name`, Expected: []sql.Row{{"inherit_guard_child", "YES"}, {"inherit_guard_parent", "YES"}, {"inherit_required_child", "NO"}, {"inherit_required_parent", "NO"}}},
				{Query: `SELECT count(*) FROM inherit_guard_schema.child`, Expected: []sql.Row{{int64(0)}}},
			},
		},
	})
}

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

func TestInheritedTableReads(t *testing.T) {
	RunScripts(t, []ScriptTest{
		{
			Name: "parent scans include child rows while ONLY and INSERT remain physical",
			SetUpScript: []string{
				`CREATE TABLE read_parent (id INT PRIMARY KEY, payload TEXT)`,
				`CREATE TABLE read_child (child_value INT, PRIMARY KEY (id)) INHERITS (read_parent)`,
				// Propagation appends late_col after the child's own column, so the
				// inherited scan must map columns by identity rather than ordinal.
				`ALTER TABLE read_parent ADD COLUMN late_col TEXT`,
				`INSERT INTO read_parent VALUES (1, 'parent', 'parent-late')`,
				`INSERT INTO read_child VALUES (1, 'child', 10, 'child-late'), (2, 'child-two', 20, 'child-two-late')`,
			},
			Assertions: []ScriptTestAssertion{
				{Query: `SELECT id, payload, late_col FROM read_parent ORDER BY payload`, Expected: []sql.Row{{1, "child", "child-late"}, {2, "child-two", "child-two-late"}, {1, "parent", "parent-late"}}},
				{Query: `SELECT count(*), count(DISTINCT id) FROM read_parent`, Expected: []sql.Row{{int64(3), int64(2)}}},
				{Query: `SELECT id FROM ONLY read_parent ORDER BY id`, Expected: []sql.Row{{1}}},
				{Query: `SELECT read_parent.id FROM ONLY read_parent`, Expected: []sql.Row{{1}}},
				{Query: `SELECT p.id FROM ONLY read_parent AS p`, Expected: []sql.Row{{1}}},
				{Query: `SELECT p.id, c.child_value FROM read_parent p JOIN ONLY read_child c ON p.id = c.id ORDER BY p.payload`, Expected: []sql.Row{{1, 10}, {2, 20}, {1, 10}}},
				{Query: `INSERT INTO read_parent VALUES (3, 'new-parent', 'new-late')`},
				{Query: `INSERT INTO read_parent SELECT 4, payload, late_col FROM read_parent WHERE id = 2`},
				{Query: `SELECT id FROM ONLY read_parent ORDER BY id`, Expected: []sql.Row{{1}, {3}, {4}}},
				{Query: `SELECT count(*) FROM ONLY read_child`, Expected: []sql.Row{{int64(2)}}},
			},
		},
		{
			Name: "incompatible common inherited columns fail before table creation",
			SetUpScript: []string{
				`CREATE TABLE conflict_parent_int (common_col INT)`,
				`CREATE TABLE conflict_parent_text (common_col TEXT)`,
				`CREATE TABLE conflict_parent_upper ("CaseCol" INT)`,
				`CREATE TABLE conflict_parent_lower ("casecol" INT)`,
			},
			Assertions: []ScriptTestAssertion{
				{Query: `CREATE TABLE conflict_child () INHERITS (conflict_parent_int, conflict_parent_text)`, ExpectedErr: `inherited column "common_col" has incompatible types`},
				{Query: `SELECT to_regclass('conflict_child') IS NULL`, Expected: []sql.Row{{"t"}}},
				{Query: `CREATE TABLE conflict_override (common_col TEXT) INHERITS (conflict_parent_int)`, ExpectedErr: `inherited column "common_col" has incompatible types`},
				{Query: `SELECT to_regclass('conflict_override') IS NULL`, Expected: []sql.Row{{"t"}}},
				{Query: `CREATE TABLE conflict_case () INHERITS (conflict_parent_upper, conflict_parent_lower)`, ExpectedErr: `differ only by case`},
				{Query: `SELECT to_regclass('conflict_case') IS NULL`, Expected: []sql.Row{{"t"}}},
			},
		},
		{
			Name: "nested diamond scans each descendant once and maps child schemas",
			SetUpScript: []string{
				`CREATE TABLE diamond_root (id INT, root_value TEXT)`,
				`CREATE TABLE diamond_left (left_value TEXT) INHERITS (diamond_root)`,
				`CREATE TABLE diamond_right (right_value TEXT) INHERITS (diamond_root)`,
				`CREATE TABLE diamond_leaf (leaf_value TEXT) INHERITS (diamond_left, diamond_right)`,
				`INSERT INTO diamond_root VALUES (1, 'root')`,
				`INSERT INTO diamond_left VALUES (2, 'left', 'left-only')`,
				`INSERT INTO diamond_right VALUES (3, 'right', 'right-only')`,
				`INSERT INTO diamond_leaf VALUES (4, 'leaf', 'leaf-left', 'leaf-right', 'leaf-only')`,
			},
			Assertions: []ScriptTestAssertion{
				{Query: `SELECT id, root_value FROM diamond_root ORDER BY id`, Expected: []sql.Row{{1, "root"}, {2, "left"}, {3, "right"}, {4, "leaf"}}},
				{Query: `SELECT count(*), count(DISTINCT id) FROM diamond_root`, Expected: []sql.Row{{int64(4), int64(4)}}},
				{Query: `SELECT id, left_value FROM diamond_left ORDER BY id`, Expected: []sql.Row{{2, "left-only"}, {4, "leaf-left"}}},
				{Query: `SELECT * FROM ONLY diamond_leaf`, Expected: []sql.Row{{4, "leaf", "leaf-left", "leaf-right", "leaf-only"}}},
			},
		},
		{
			Name: "schema-qualified quoted inheritance and pg_inherits edges",
			SetUpScript: []string{
				`CREATE SCHEMA "Family"`,
				`CREATE TABLE "Family"."Parent" ("ID" INT)`,
				`CREATE TABLE "Family"."Child" ("Note" TEXT) INHERITS ("Family"."Parent")`,
				`INSERT INTO "Family"."Parent" VALUES (1)`,
				`INSERT INTO "Family"."Child" VALUES (2, 'child')`,
			},
			Assertions: []ScriptTestAssertion{
				{Query: `SELECT "ID" FROM "Family"."Parent" ORDER BY "ID"`, Expected: []sql.Row{{1}, {2}}},
				{Query: `SELECT "ID" FROM ONLY "Family"."Parent"`, Expected: []sql.Row{{1}}},
				{
					Query: `SELECT child_ns.nspname, child.relname, parent_ns.nspname, parent.relname
FROM pg_inherits i
JOIN pg_class child ON child.oid = i.inhrelid
JOIN pg_namespace child_ns ON child_ns.oid = child.relnamespace
JOIN pg_class parent ON parent.oid = i.inhparent
JOIN pg_namespace parent_ns ON parent_ns.oid = parent.relnamespace
WHERE child_ns.nspname = 'Family' AND child.relname = 'Child'`,
					Expected: []sql.Row{{"Family", "Child", "Family", "Parent"}},
				},
				{Query: `SELECT a.attname FROM pg_attribute a JOIN pg_class c ON c.oid = a.attrelid JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = 'Family' AND c.relname = 'Child' AND a.attnum > 0 ORDER BY a.attnum`, Expected: []sql.Row{{"ID"}, {"Note"}}},
			},
		},
		{
			Name: "transaction rollback savepoints and IF NOT EXISTS preserve exact metadata",
			SetUpScript: []string{
				`CREATE TABLE metadata_parent_one (id INT)`,
				`CREATE TABLE metadata_parent_two (id INT)`,
				`CREATE TABLE metadata_child () INHERITS (metadata_parent_one)`,
			},
			Assertions: []ScriptTestAssertion{
				{Query: `CREATE TABLE IF NOT EXISTS metadata_child () INHERITS (metadata_parent_two)`},
				{Query: `SELECT count(*) FROM pg_inherits WHERE inhrelid = 'metadata_child'::regclass`, Expected: []sql.Row{{int64(1)}}},
				{Query: `SELECT inhparent = 'metadata_parent_one'::regclass FROM pg_inherits WHERE inhrelid = 'metadata_child'::regclass`, Expected: []sql.Row{{"t"}}},
				{Query: `BEGIN`},
				{Query: `CREATE TABLE rollback_child () INHERITS (metadata_parent_one)`},
				{Query: `ROLLBACK`},
				{Query: `SELECT count(*) FROM pg_inherits WHERE inhrelid = to_regclass('rollback_child')`, Expected: []sql.Row{{int64(0)}}},
				{Query: `BEGIN`},
				{Query: `SAVEPOINT before_child`},
				{Query: `CREATE TABLE savepoint_child () INHERITS (metadata_parent_one)`},
				{Query: `ROLLBACK TO SAVEPOINT before_child`},
				{Query: `RELEASE SAVEPOINT before_child`},
				{Query: `COMMIT`},
				{Query: `SELECT count(*) FROM pg_inherits WHERE inhrelid = to_regclass('savepoint_child')`, Expected: []sql.Row{{int64(0)}}},
			},
		},
		{
			Name: "inheritance graph mutations are guarded while leaf-owned updates work",
			SetUpScript: []string{
				`CREATE TABLE mutation_parent (id INT, value TEXT)`,
				`CREATE TABLE mutation_child (own_value INT) INHERITS (mutation_parent)`,
				`INSERT INTO mutation_parent VALUES (1, 'parent')`,
				`INSERT INTO mutation_child VALUES (2, 'child', 10)`,
			},
			Assertions: []ScriptTestAssertion{
				{Query: `UPDATE mutation_parent SET value = 'changed'`, ExpectedErr: `inherited UPDATE/DELETE is not yet supported`},
				{Query: `DELETE FROM mutation_parent`, ExpectedErr: `inherited UPDATE/DELETE is not yet supported`},
				{Query: `TRUNCATE mutation_parent`, ExpectedErr: `schema or truncate operation on inherited table`},
				{Query: `ALTER TABLE mutation_parent ALTER COLUMN value SET DEFAULT 'blocked'`, ExpectedErr: `schema or truncate operation on inherited table`},
				{Query: `ALTER TABLE ONLY mutation_parent ALTER COLUMN value SET DEFAULT 'blocked'`, ExpectedErr: `schema or truncate operation on inherited table`},
				{Query: `ALTER TABLE mutation_child ADD CHECK (own_value > 0)`, ExpectedErr: `schema or truncate operation on inherited table`},
				{Query: `ALTER TABLE mutation_parent RENAME TO mutation_parent_renamed`, ExpectedErr: `participates in table inheritance`},
				{Query: `DROP TABLE mutation_child`, ExpectedErr: `participates in table inheritance`},
				{Query: `UPDATE ONLY mutation_child SET own_value = 11 WHERE id = 2`, ExpectedErr: `UPDATE ONLY is not yet supported`},
				{Query: `DELETE FROM ONLY mutation_child WHERE id = 2`, ExpectedErr: `DELETE ONLY is not yet supported`},
				{Query: `UPDATE mutation_child SET own_value = 11 WHERE id = 2`},
				{Query: `SELECT own_value FROM ONLY mutation_child WHERE id = 2`, Expected: []sql.Row{{11}}},
				{Query: `SELECT value FROM ONLY mutation_parent WHERE id = 1`, Expected: []sql.Row{{"parent"}}},
			},
		},
		{
			Name: "inheritance metadata follows Dolt branches and commits",
			SetUpScript: []string{
				`CREATE TABLE branch_parent (id INT)`,
				`SELECT DOLT_COMMIT('-Am', 'create parent')`,
				`SELECT DOLT_CHECKOUT('-b', 'inheritance_branch')`,
				`CREATE TABLE branch_child () INHERITS (branch_parent)`,
				`SELECT DOLT_COMMIT('-Am', 'create inherited child')`,
			},
			Assertions: []ScriptTestAssertion{
				{Query: `SELECT count(*) FROM pg_inherits WHERE inhrelid = 'branch_child'::regclass`, Expected: []sql.Row{{int64(1)}}},
				{Query: `SELECT DOLT_CHECKOUT('main')`, SkipResultsCheck: true},
				{Query: `SELECT to_regclass('branch_child') IS NULL`, Expected: []sql.Row{{"t"}}},
				{Query: `SELECT count(*) FROM pg_inherits`, Expected: []sql.Row{{int64(0)}}},
				{Query: `SELECT DOLT_CHECKOUT('inheritance_branch')`, SkipResultsCheck: true},
				{Query: `SELECT count(*) FROM pg_inherits WHERE inhrelid = 'branch_child'::regclass`, Expected: []sql.Row{{int64(1)}}},
			},
		},
	})
}

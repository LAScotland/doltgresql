// Copyright 2026 Dolthub, Inc.
// Licensed under the Apache License, Version 2.0

package _go

import (
	"testing"

	"github.com/dolthub/go-mysql-server/sql"
)

func TestExpressionIndexCheckEnforcement(t *testing.T) {
	RunScripts(t, []ScriptTest{
		{
			Name: "expression index preserves CHECK enforcement for inserts and updates",
			SetUpScript: []string{
				`CREATE TABLE expression_check (id INT PRIMARY KEY, a INT, b INT, CONSTRAINT positive_values CHECK (a > 0 AND b > 0))`,
				`CREATE INDEX expression_check_sum ON expression_check ((a + b))`,
			},
			Assertions: []ScriptTestAssertion{
				{Query: `INSERT INTO expression_check VALUES (1, -1, 2)`, ExpectedErr: `Check constraint "positive_values" violated`},
				{Query: `SELECT count(*) FROM expression_check`, Expected: []sql.Row{{int64(0)}}},
				{Query: `INSERT INTO expression_check VALUES (2, 1, 2), (3, NULL, 2)`},
				{Query: `UPDATE expression_check SET a = -1 WHERE id = 2`, ExpectedErr: `Check constraint "positive_values" violated`},
				{Query: `SELECT a FROM expression_check WHERE id = 2`, Expected: []sql.Row{{int32(1)}}},
				{Query: `INSERT INTO expression_check VALUES (4, 4, 4), (5, -5, 5)`, ExpectedErr: `Check constraint "positive_values" violated`},
				{Query: `SELECT count(*) FROM expression_check WHERE id IN (4, 5)`, Expected: []sql.Row{{int64(0)}}},
				{Query: `SELECT id FROM expression_check WHERE a + b = 3`, Expected: []sql.Row{{2}}},
			},
		},
		{
			Name: "UPDATE FROM attaches only the aliased target checks",
			SetUpScript: []string{
				`CREATE TABLE expression_check_target (id INT PRIMARY KEY, a INT, CONSTRAINT target_positive CHECK (a > 0))`,
				`CREATE INDEX expression_check_target_abs ON expression_check_target ((abs(a)))`,
				`CREATE TABLE expression_check_source (id INT PRIMARY KEY, a INT, CONSTRAINT source_negative CHECK (a < 0))`,
				`INSERT INTO expression_check_target VALUES (1, 1), (2, 2)`,
				`INSERT INTO expression_check_source VALUES (1, -10), (2, -20)`,
			},
			Assertions: []ScriptTestAssertion{
				{Query: `UPDATE expression_check_target AS target SET a = abs(source.a) FROM expression_check_source AS source WHERE target.id = source.id`},
				{Query: `SELECT id, a FROM expression_check_target ORDER BY id`, Expected: []sql.Row{{1, 10}, {2, 20}}},
				{Query: `UPDATE expression_check_target AS target SET a = source.a FROM expression_check_source AS source WHERE target.id = source.id`, ExpectedErr: `Check constraint "target_positive" violated`},
				{Query: `SELECT id, a FROM expression_check_target ORDER BY id`, Expected: []sql.Row{{1, 10}, {2, 20}}},
			},
		},
		{
			Name: "schema-qualified quoted CHECK expressions bind to the target",
			SetUpScript: []string{
				`CREATE SCHEMA "Odd Schema"`,
				`CREATE TABLE "Odd Schema"."Check Table" ("ID" INT PRIMARY KEY, "A" INT, CONSTRAINT "A Positive" CHECK ("A" > 0))`,
				`CREATE INDEX "Check Expr" ON "Odd Schema"."Check Table" (("A" + 1))`,
			},
			Assertions: []ScriptTestAssertion{
				{Query: `INSERT INTO "Odd Schema"."Check Table" VALUES (1, -1)`, ExpectedErr: `Check constraint "A Positive" violated`},
				{Query: `INSERT INTO "Odd Schema"."Check Table" VALUES (1, 3)`},
				{Query: `UPDATE "Odd Schema"."Check Table" SET "A" = 0 WHERE "ID" = 1`, ExpectedErr: `Check constraint "A Positive" violated`},
				{Query: `SELECT "A" FROM "Odd Schema"."Check Table" WHERE "A" + 1 = 4`, Expected: []sql.Row{{int32(3)}}},
			},
		},
	})
}

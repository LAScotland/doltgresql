// Copyright 2026 Dolthub, Inc.
// Licensed under the Apache License, Version 2.0 (the "License").
package _go

import (
	"github.com/dolthub/go-mysql-server/sql"
	"testing"
)

func TestInheritancePrivileges(t *testing.T) {
	RunScripts(t, []ScriptTest{{
		Name: "inherited scans check the named parent's permission",
		SetUpScript: []string{
			`CREATE USER inheritance_reader PASSWORD 'synthetic'`,
			`GRANT USAGE ON SCHEMA public TO inheritance_reader`,
			`CREATE TABLE permission_parent (id INT)`,
			`CREATE TABLE permission_child () INHERITS (permission_parent)`,
			`INSERT INTO permission_parent VALUES (1)`,
			`INSERT INTO permission_child VALUES (2)`,
			`GRANT SELECT ON permission_parent TO inheritance_reader`,
		},
		Assertions: []ScriptTestAssertion{
			{Query: `SELECT id FROM permission_parent ORDER BY id`, Username: "inheritance_reader", Password: "synthetic", Expected: []sql.Row{{1}, {2}}},
			{Query: `SELECT id FROM ONLY permission_parent`, Username: "inheritance_reader", Password: "synthetic", Expected: []sql.Row{{1}}},
			{Query: `SELECT id FROM permission_child`, Username: "inheritance_reader", Password: "synthetic", ExpectedErr: "denied"},
			{Query: `SELECT id FROM ONLY permission_child`, Username: "inheritance_reader", Password: "synthetic", ExpectedErr: "denied"},
			{Query: `REVOKE SELECT ON permission_parent FROM inheritance_reader`},
			{Query: `SELECT id FROM permission_parent`, Username: "inheritance_reader", Password: "synthetic", ExpectedErr: "denied"},
			{Query: `SELECT id FROM ONLY permission_parent`, Username: "inheritance_reader", Password: "synthetic", ExpectedErr: "denied"},
		},
	}})
}

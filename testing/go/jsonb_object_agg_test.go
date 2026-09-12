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

func TestJsonbObjectAgg(t *testing.T) {
	RunScripts(t, []ScriptTest{{
		Name: "jsonb_object_agg PostgreSQL behavior",
		SetUpScript: []string{
			`CREATE TABLE object_agg_values (ord int4 primary key, k text, v int4, payload jsonb);`,
			`INSERT INTO object_agg_values VALUES (1,'a',1,'{"nested":true}'), (2,'b',NULL,'null'), (3,'a',3,'[1,2]');`,
		},
		Assertions: []ScriptTestAssertion{
			{Query: `SELECT jsonb_object_agg(k, v) FROM object_agg_values;`, Expected: []sql.Row{{`{"a": 3, "b": null}`}}},
			{Query: `SELECT jsonb_object_agg(k, payload) FROM object_agg_values;`, Expected: []sql.Row{{`{"a": [1, 2], "b": null}`}}},
			{Query: `SELECT jsonb_object_agg(k, v ORDER BY ord DESC) FROM object_agg_values;`, ExpectedErr: `function ORDER BY is not yet supported`},
			{Query: `SELECT jsonb_object_agg(k, v) FROM (SELECT * FROM object_agg_values WHERE false) x;`, Expected: []sql.Row{{nil}}},
			{Query: `SELECT jsonb_object_agg(k, v) FROM (VALUES (NULL::text, 1)) AS x(k,v);`, ExpectedErr: `field name must not be null`},
			{Query: `SELECT jsonb_object_agg(k, v) OVER (ORDER BY ord ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) FROM object_agg_values ORDER BY ord;`, Expected: []sql.Row{{`{"a": 1}`}, {`{"a": 1, "b": null}`}, {`{"a": 3, "b": null}`}}},
			{Query: `SELECT jsonb_object_agg(k, CASE WHEN v IS NULL THEN 0 ELSE v::int4 END) FROM object_agg_values;`, Expected: []sql.Row{{`{"a": 3, "b": 0}`}}},
			{Query: `SELECT jsonb_object_agg(k, v) FILTER (WHERE v IS NOT NULL) FROM object_agg_values;`, ExpectedErr: `function filters are not yet supported`},
			{Query: `SELECT jsonb_object_agg(k, v) FROM (VALUES (true,1),(false,2)) AS x(k,v);`, Expected: []sql.Row{{`{"true": 1, "false": 2}`}}},
			{Query: `SELECT jsonb_object_agg(k, 1) FROM (VALUES (ARRAY[1])) AS x(k);`, ExpectedErr: `key value must be scalar, not array, composite, or json`},
			{Query: `SELECT jsonb_object_agg(k, 1) FROM (VALUES ('"a"'::jsonb)) AS x(k);`, ExpectedErr: `key value must be scalar, not array, composite, or json`},
			{Query: `SELECT jsonb_object_agg(k, v) FROM (VALUES (2::numeric,'two'::text),(10::numeric,'ten'::text)) AS x(k,v);`, Expected: []sql.Row{{`{"2": "two", "10": "ten"}`}}},
			{Query: `SELECT jsonb_object_agg(k, v) FROM (VALUES ('numeric'::text,123.45::numeric)) AS x(k,v);`, Expected: []sql.Row{{`{"numeric": 123.45}`}}},
			{Query: `SELECT jsonb_object_agg('literal', 7);`, Expected: []sql.Row{{`{"literal": 7}`}}},
			{Query: `SELECT g, jsonb_object_agg(k,v) FROM (VALUES ('x','a',1),('x','b',2),('y','c',3)) AS x(g,k,v) GROUP BY g ORDER BY g;`, Expected: []sql.Row{{"x", `{"a": 1, "b": 2}`}, {"y", `{"c": 3}`}}},
			{Query: `SELECT jsonb_object_agg(k,v) OVER (ORDER BY ord ROWS BETWEEN 1 PRECEDING AND 1 PRECEDING) FROM (VALUES (1,'a',1),(2,'b',2)) AS x(ord,k,v) ORDER BY ord;`, Expected: []sql.Row{{nil}, {`{"a": 1}`}}},
			{Query: `SELECT (jsonb_object_agg(k,v) ->> 'a') FROM object_agg_values;`, Expected: []sql.Row{{"3"}}},
			{Query: `SELECT e.key, e.value FROM (SELECT jsonb_object_agg(k,v) AS obj FROM object_agg_values) a CROSS JOIN LATERAL jsonb_each_text(a.obj) e ORDER BY e.key;`, Expected: []sql.Row{{"a", "3"}, {"b", nil}}},
			{Query: `SELECT jsonb_object_agg(k,v) @? '$.* ? (@ == 3)' FROM object_agg_values;`, Expected: []sql.Row{{"t"}}},
		},
	}})
}

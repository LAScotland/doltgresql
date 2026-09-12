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
	"github.com/dolthub/go-mysql-server/sql"
	"testing"
)

func TestLockTimeoutDuration(t *testing.T) {
	RunScripts(t, []ScriptTest{{Name: "lock_timeout duration input", Assertions: []ScriptTestAssertion{
		{Query: `SET SESSION lock_timeout = '15s';`},
		{Query: `SHOW lock_timeout;`, Expected: []sql.Row{{15000}}},
		{Query: `SELECT current_setting('lock_timeout');`, Expected: []sql.Row{{"15000"}}},
		{Query: `SELECT setting, unit FROM pg_settings WHERE name = 'lock_timeout';`, Expected: []sql.Row{{"15000", "ms"}}},
		{Query: `BEGIN;`},
		{Query: `SET LOCAL lock_timeout = '1.5s';`},
		{Query: `SHOW lock_timeout;`, Expected: []sql.Row{{1500}}},
		{Query: `COMMIT;`},
		{Query: `SHOW lock_timeout;`, Expected: []sql.Row{{15000}}},
		{Query: `SELECT set_config('lock_timeout', '2 min', false);`, Expected: []sql.Row{{"2 min"}}},
		{Query: `SHOW lock_timeout;`, Expected: []sql.Row{{120000}}},
		{Query: `SET lock_timeout = '1500us';`},
		{Query: `SHOW lock_timeout;`, Expected: []sql.Row{{2}}},
		{Query: `SET lock_timeout = 'bogus';`, ExpectedErr: "can't be set"},
		{Query: `SET lock_timeout = '-1ms';`, ExpectedErr: "can't be set"},
		{Query: `SET lock_timeout = '2147483648ms';`, ExpectedErr: "can't be set"},
		{Query: `SHOW lock_timeout;`, Expected: []sql.Row{{2}}},
		{Query: `RESET lock_timeout;`},
		{Query: `SHOW lock_timeout;`, Expected: []sql.Row{{0}}},
	}}})
}

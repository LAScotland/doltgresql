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

package config

import "testing"

func TestParseMilliseconds(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  int64
	}{
		{"0", 0}, {"15s", 15000}, {" 2 min ", 120000}, {"1h", 3600000}, {"1d", 86400000},
		{"1500us", 2}, {"2500us", 2}, {"1.5s", 1500}, {"1.2345s", 1234},
		{"0.001min", 0}, {"0.5", 0}, {"1e2 ms", 100}, {"2147483647ms", 2147483647},
		{"-1ms", -1}, {"1.5ms", 2}, {"2.5ms", 2},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got, err := parseMilliseconds("lock_timeout", tc.input)
			if err != nil || got != tc.want {
				t.Fatalf("got %d, %v; want %d", got, err, tc.want)
			}
		})
	}
	for _, input := range []string{"", "bogus", "1sec", "1S", "NaN", "Inf", "1e999s", "2147483648ms", "-2147483649ms", "1 s extra"} {
		t.Run("invalid_"+input, func(t *testing.T) {
			if _, err := parseMilliseconds("lock_timeout", input); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

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

import (
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/dolthub/go-mysql-server/sql"
)

var timeParameterPattern = regexp.MustCompile(`^([+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?)[ \t]*(us|ms|s|min|h|d)?$`)

// parseMilliseconds converts PostgreSQL time-parameter input into integer milliseconds.
// Values with explicit units are first rounded to the next smaller supported unit,
// then to milliseconds, using ties-to-even rounding as PostgreSQL does.
// This is input conversion, not an implementation of timeout enforcement.
func parseMilliseconds(name, input string) (int64, error) {
	invalid := func() (int64, error) { return 0, sql.ErrInvalidSystemVariableValue.New(name, input) }
	parts := timeParameterPattern.FindStringSubmatch(strings.TrimSpace(input))
	if parts == nil {
		return invalid()
	}
	value, err := strconv.ParseFloat(parts[1], 64)
	if err != nil || math.IsInf(value, 0) || math.IsNaN(value) {
		return invalid()
	}
	multiplier, quantum := 1.0, 0.0
	switch parts[2] {
	case "us":
		multiplier = 0.001
	case "ms":
		quantum = 0.001
	case "s":
		multiplier, quantum = 1000, 1
	case "min":
		multiplier, quantum = 60000, 1000
	case "h":
		multiplier, quantum = 3600000, 60000
	case "d":
		multiplier, quantum = 86400000, 3600000
	}
	value *= multiplier
	if quantum != 0 {
		value = math.RoundToEven(value/quantum) * quantum
	}
	value = math.RoundToEven(value)
	if math.IsInf(value, 0) || value < math.MinInt32 || value > math.MaxInt32 {
		return invalid()
	}
	return int64(value), nil
}

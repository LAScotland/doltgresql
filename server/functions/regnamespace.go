// Copyright 2024 Dolthub, Inc.
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

package functions

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/cockroachdb/errors"
	"github.com/dolthub/doltgresql/core/id"
	"github.com/dolthub/doltgresql/server/functions/framework"
	pgtypes "github.com/dolthub/doltgresql/server/types"
	"github.com/dolthub/go-mysql-server/sql"
)

func initRegnamespace() {
	framework.RegisterFunction(regnamespacein)
	framework.RegisterFunction(regnamespaceout)
	framework.RegisterFunction(regnamespacerecv)
	framework.RegisterFunction(regnamespacesend)
	framework.RegisterFunction(to_regnamespace)
}

var regnamespacein = framework.Function1{
	Name: "regnamespacein", Return: pgtypes.Regnamespace,
	Parameters: [1]*pgtypes.DoltgresType{pgtypes.Cstring}, Strict: true,
	IsNonDeterministic: true,
	Callable: func(ctx *sql.Context, _ [2]*pgtypes.DoltgresType, val any) (any, error) {
		input, err := framework.UnwrapString(ctx, val)
		if err != nil {
			return nil, err
		}
		return regnamespaceInput(ctx, input, false)
	},
}

// regnamespaceInput resolves a single schema name, independent of search_path.
// missingOK suppresses invalid input and missing names for to_regnamespace, but
// preserves errors encountered while accessing the database.
func regnamespaceInput(ctx *sql.Context, input string, missingOK bool) (any, error) {
	invalid := func(err error) (any, error) {
		if missingOK {
			return nil, nil
		}
		return nil, err
	}
	if input == "-" {
		return id.NewOID(0).AsId(), nil
	}
	if input != "" && strings.Trim(input, "0123456789") == "" {
		n, err := strconv.ParseUint(input, 10, 32)
		if err != nil {
			return invalid(pgtypes.ErrValueIsOutOfRangeForType.New(input, "oid"))
		}
		internal := id.Cache().ToInternal(uint32(n))
		if internal.IsValid() {
			return internal, nil
		}
		return id.NewOID(uint32(n)).AsId(), nil
	}
	name, err := regnamespaceName(input)
	if err != nil {
		return invalid(err)
	}
	var result id.Id
	err = IterateCurrentDatabase(ctx, Callbacks{
		Schema: func(ctx *sql.Context, schema ItemSchema) (bool, error) {
			if schema.Item.SchemaName() == name {
				result = schema.OID.AsId()
				return false, nil
			}
			return true, nil
		},
	})
	if err != nil {
		return nil, err
	}
	if result.IsValid() {
		return result, nil
	}
	return invalid(errors.Errorf(`schema "%s" does not exist`, name))
}

// regnamespaceName parses one identifier; unlike regclass, qualified names are invalid.
func regnamespaceName(input string) (string, error) {
	s := strings.TrimSpace(input)
	invalid := func() (string, error) { return "", errors.Errorf("invalid name syntax") }
	if s == "" {
		return invalid()
	}
	if s[0] == '"' {
		var b strings.Builder
		for i := 1; i < len(s); i++ {
			if s[i] != '"' {
				b.WriteByte(s[i])
				continue
			}
			if i+1 < len(s) && s[i+1] == '"' {
				b.WriteByte('"')
				i++
				continue
			}
			if i != len(s)-1 || b.Len() == 0 {
				return invalid()
			}
			return b.String(), nil
		}
		return invalid()
	}
	for _, r := range s {
		if unicode.IsSpace(r) || r == '.' || r == '"' {
			return invalid()
		}
	}
	return strings.ToLower(s), nil
}

var regnamespaceout = framework.Function1{
	Name: "regnamespaceout", Return: pgtypes.Cstring,
	Parameters: [1]*pgtypes.DoltgresType{pgtypes.Regnamespace}, Strict: true,
	IsNonDeterministic: true,
	Callable: func(ctx *sql.Context, _ [2]*pgtypes.DoltgresType, val any) (any, error) {
		oid := id.Cache().ToOID(val.(id.Id))
		if oid == 0 {
			return "-", nil
		}
		result := strconv.FormatUint(uint64(oid), 10)
		err := IterateCurrentDatabase(ctx, Callbacks{
			Schema: func(ctx *sql.Context, schema ItemSchema) (bool, error) {
				if id.Cache().ToOID(schema.OID.AsId()) == oid {
					result = quoteTypeIdentifier(schema.Item.SchemaName())
					return false, nil
				}
				return true, nil
			},
		})
		return result, err
	},
}

var regnamespacerecv = framework.Function1{
	Name: "regnamespacerecv", Return: pgtypes.Regnamespace,
	Parameters: [1]*pgtypes.DoltgresType{pgtypes.Internal}, Strict: true,
	Callable: oidrecv.Callable,
}
var regnamespacesend = framework.Function1{
	Name: "regnamespacesend", Return: pgtypes.Bytea,
	Parameters: [1]*pgtypes.DoltgresType{pgtypes.Regnamespace}, Strict: true,
	Callable: oidsend.Callable,
}
var to_regnamespace = framework.Function1{
	Name: "to_regnamespace", Return: pgtypes.Regnamespace,
	Parameters: [1]*pgtypes.DoltgresType{pgtypes.Text}, Strict: true,
	IsNonDeterministic: true,
	Callable: func(ctx *sql.Context, _ [2]*pgtypes.DoltgresType, val any) (any, error) {
		input, err := framework.UnwrapString(ctx, val)
		if err != nil {
			return nil, err
		}
		return regnamespaceInput(ctx, input, true)
	},
}

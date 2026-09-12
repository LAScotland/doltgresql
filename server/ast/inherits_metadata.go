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

package ast

import (
	"context"

	gmsexpression "github.com/dolthub/go-mysql-server/sql/expression"

	pgtypes "github.com/dolthub/doltgresql/server/types"
)

// InheritsMetadata is a typed, planner-only carrier for information that GMS's
// CREATE TABLE LIKE planner otherwise discards. It is placed on a temporary
// synthetic column and removed by normalizeCreateTableInherits before normal
// CREATE TABLE validation. This bridge can go away when GMS distinguishes
// PostgreSQL INHERITS from CREATE TABLE LIKE in its plan API.
type InheritsMetadata struct {
	PrimaryKeyColumns []string
}

// WithResolvedChildren implements vitess.Injectable.
func (m *InheritsMetadata) WithResolvedChildren(context.Context, []any) (any, error) {
	return &InheritsMetadataExpression{
		Literal:           gmsexpression.NewLiteral(int32(0), pgtypes.Int32),
		PrimaryKeyColumns: append([]string(nil), m.PrimaryKeyColumns...),
	}, nil
}

// InheritsMetadataExpression has a concrete Go type so SQL text cannot spoof
// the marker. Literal supplies the leaf sql.Expression implementation.
type InheritsMetadataExpression struct {
	*gmsexpression.Literal
	PrimaryKeyColumns []string
}

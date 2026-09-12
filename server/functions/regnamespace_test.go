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

package functions

import (
	"encoding/binary"
	"testing"

	"github.com/dolthub/go-mysql-server/sql"
	"github.com/stretchr/testify/require"

	"github.com/dolthub/doltgresql/core/id"
	pgtypes "github.com/dolthub/doltgresql/server/types"
)

func TestRegnamespaceBinaryIO(t *testing.T) {
	ctx := sql.NewEmptyContext()
	for _, oid := range []uint32{0, 11, 4294967295} {
		data := make([]byte, 4)
		binary.BigEndian.PutUint32(data, oid)
		value, err := regnamespacerecv.Callable(ctx, [2]*pgtypes.DoltgresType{}, data)
		require.NoError(t, err)
		require.Equal(t, oid, id.Cache().ToOID(value.(id.Id)))
		encoded, err := regnamespacesend.Callable(ctx, [2]*pgtypes.DoltgresType{}, value)
		require.NoError(t, err)
		require.Equal(t, data, encoded)
	}
}

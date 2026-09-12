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

package inheritance

import (
	"context"
	"testing"

	"github.com/dolthub/dolt/go/libraries/doltcore/doltdb"
	"github.com/dolthub/dolt/go/store/prolly/tree"
	"github.com/dolthub/doltgresql/core/id"
	"github.com/dolthub/doltgresql/core/rootobject/objinterface"
	"github.com/stretchr/testify/require"
)

func newTestCollection(t *testing.T) *Collection {
	t.Helper()
	rom, err := objinterface.NewDetachedRootObjectMap(storage, tree.NewTestNodeStore())
	require.NoError(t, err)
	c, err := NewCollection(context.Background(), rom)
	require.NoError(t, err)
	return c
}

func TestInheritanceGraph(t *testing.T) {
	ctx := context.Background()
	c := newTestCollection(t)
	p := id.NewTable("public", "parent")
	c1 := id.NewTable("public", "child1")
	c2 := id.NewTable("public", "child2")
	g := id.NewTable("public", "grandchild")
	require.NoError(t, c.SetParents(ctx, c2, []id.Table{p}))
	require.NoError(t, c.SetParents(ctx, c1, []id.Table{p}))
	require.NoError(t, c.SetParents(ctx, g, []id.Table{c1, c2}))
	require.Equal(t, []id.Table{c1, c2}, c.DirectChildren(ctx, p))
	require.Equal(t, []id.Table{c1, c2, g}, c.Descendants(ctx, p))
	require.Equal(t, []id.Table{c1, c2}, c.GetParents(ctx, g))
	require.ErrorContains(t, c.SetParents(ctx, p, []id.Table{g}), "cycle")
	require.ErrorContains(t, c.SetParents(ctx, c1, []id.Table{p, p}), "duplicate")
	require.ErrorContains(t, c.SetParents(ctx, p, []id.Table{p}), "itself")
}

func TestInheritanceRenameAndRemove(t *testing.T) {
	ctx := context.Background()
	c := newTestCollection(t)
	p := id.NewTable("a", "p")
	np := id.NewTable("b", "renamed")
	child := id.NewTable("a", "c")
	require.NoError(t, c.SetParents(ctx, child, []id.Table{p}))
	require.NoError(t, c.RenameTable(ctx, p, np))
	require.Equal(t, []id.Table{np}, c.GetParents(ctx, child))
	require.NoError(t, c.RenameTable(ctx, child, id.NewTable("b", "c2")))
	require.Empty(t, c.GetParents(ctx, child))
	require.Len(t, c.Descendants(ctx, np), 1)
	require.NoError(t, c.RemoveChild(ctx, id.NewTable("b", "c2")))
	require.Empty(t, c.Descendants(ctx, np))
}

func TestInheritanceSerializationAndConflict(t *testing.T) {
	ctx := context.Background()
	child := id.NewTable("public", "c")
	p1 := id.NewTable("public", "p1")
	p2 := id.NewTable("public", "p2")
	edge := Edge{Child: child, Parents: []id.Table{p1, p2}}
	data, err := edge.Serialize(ctx)
	require.NoError(t, err)
	decoded, err := DeserializeEdge(ctx, data)
	require.NoError(t, err)
	require.Equal(t, edge, decoded)
	c := newTestCollection(t)
	internalName := inheritanceObjectName(child)
	require.Equal(t, id.NewInheritance("public", "c").AsId(), c.TableNameToID(internalName))
	require.Equal(t, id.Null, c.TableNameToID(doltdb.TableName{Schema: "other", Name: internalName.Name}))
	diffs, merged, err := c.DiffRootObjects(ctx, "base", Edge{Child: child, Parents: []id.Table{p1}}, Edge{Child: child, Parents: []id.Table{p2}}, edge)
	require.NoError(t, err)
	require.Nil(t, merged)
	require.Len(t, diffs, 1)
	require.Equal(t, `["public.p1","public.p2"]`, diffs[0].AncestorValue)
	require.Equal(t, `["public.p1"]`, diffs[0].OurValue)
	require.Equal(t, `["public.p2"]`, diffs[0].TheirValue)
}

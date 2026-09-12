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
	"sort"

	"github.com/cockroachdb/errors"
	"github.com/dolthub/dolt/go/libraries/doltcore/doltdb"
	"github.com/dolthub/dolt/go/store/hash"
	"github.com/dolthub/dolt/go/store/prolly"

	"github.com/dolthub/doltgresql/core/id"
	"github.com/dolthub/doltgresql/core/rootobject/objinterface"
)

type Edge struct {
	Child   id.Table
	Parents []id.Table
}
type Collection struct {
	objinterface.RootObjectMap
	edges map[id.Table]Edge
}

var _ objinterface.Collection = (*Collection)(nil)
var _ objinterface.RootObject = Edge{}

func NewCollection(ctx context.Context, rom objinterface.RootObjectMap) (*Collection, error) {
	c := &Collection{RootObjectMap: rom, edges: make(map[id.Table]Edge)}
	return c, c.reload(ctx)
}
func (c *Collection) reload(ctx context.Context) error {
	clear(c.edges)
	err := c.Contents().IterAll(ctx, func(_ string, h hash.Hash) error {
		if h.IsEmpty() {
			return nil
		}
		data, err := c.NodeStore().ReadBytes(ctx, h)
		if err != nil {
			return err
		}
		edge, err := DeserializeEdge(ctx, data)
		if err != nil {
			return err
		}
		c.edges[edge.Child] = edge
		return nil
	})
	if err != nil {
		return err
	}
	for child, edge := range c.edges {
		if !validTableID(child) {
			return errors.New("invalid inheritance child")
		}
		seen := make(map[id.Table]struct{}, len(edge.Parents))
		for _, parent := range edge.Parents {
			if !validTableID(parent) {
				return errors.New("invalid inheritance parent")
			}
			if child == parent {
				return errors.Errorf("table %s cannot inherit from itself", string(child))
			}
			if _, ok := seen[parent]; ok {
				return errors.Errorf("duplicate inheritance parent %s", string(parent))
			}
			seen[parent] = struct{}{}
		}
		for _, parent := range edge.Parents {
			if c.reachable(parent, child, make(map[id.Table]bool)) {
				return errors.Errorf("inheritance cycle involving %s and %s", string(child), string(parent))
			}
		}
	}
	return nil
}
func cloneTables(v []id.Table) []id.Table { return append([]id.Table(nil), v...) }
func (c *Collection) GetParents(_ context.Context, child id.Table) []id.Table {
	return cloneTables(c.edges[child].Parents)
}
func (c *Collection) SetParents(ctx context.Context, child id.Table, parents []id.Table) error {
	if err := c.ValidateParents(child, parents); err != nil {
		return err
	}
	if len(parents) == 0 {
		return c.RemoveChild(ctx, child)
	}
	edge := Edge{Child: child, Parents: cloneTables(parents)}
	data, err := edge.Serialize(ctx)
	if err != nil {
		return err
	}
	h, err := c.NodeStore().WriteBytes(ctx, data)
	if err != nil {
		return err
	}
	ed := c.Contents().Editor()
	key := string(id.NewInheritance(child.SchemaName(), child.TableName()))
	if _, exists := c.edges[child]; exists {
		err = ed.Update(ctx, key, h)
	} else {
		err = ed.Add(ctx, key, h)
	}
	if err != nil {
		return err
	}
	m, err := ed.Flush(ctx)
	if err != nil {
		return err
	}
	return c.replaceContents(ctx, m)
}

func cloneEdges(edges map[id.Table]Edge) map[id.Table]Edge {
	cloned := make(map[id.Table]Edge, len(edges))
	for child, edge := range edges {
		edge.Parents = cloneTables(edge.Parents)
		cloned[child] = edge
	}
	return cloned
}

func (c *Collection) replaceContents(ctx context.Context, contents prolly.AddressMap) error {
	oldContents := c.Contents()
	oldEdges := cloneEdges(c.edges)
	c.SetContents(contents)
	if err := c.reload(ctx); err != nil {
		c.SetContents(oldContents)
		c.edges = oldEdges
		return err
	}
	return nil
}

// ValidateParents checks a proposed child edge without mutating the graph.
func (c *Collection) ValidateParents(child id.Table, parents []id.Table) error {
	if !validTableID(child) {
		return errors.New("invalid inheritance child")
	}
	seen := make(map[id.Table]struct{}, len(parents))
	for _, parent := range parents {
		if !validTableID(parent) {
			return errors.New("invalid inheritance parent")
		}
		if parent == child {
			return errors.Errorf("table %s cannot inherit from itself", string(child))
		}
		if _, ok := seen[parent]; ok {
			return errors.Errorf("duplicate inheritance parent %s", string(parent))
		}
		seen[parent] = struct{}{}
		if c.reachable(parent, child, make(map[id.Table]bool)) {
			return errors.Errorf("inheritance cycle involving %s and %s", string(child), string(parent))
		}
	}
	return nil
}
func validTableID(table id.Table) bool {
	return table.IsValid() && table.AsId().Section() == id.Section_Table
}
func (c *Collection) reachable(from, target id.Table, seen map[id.Table]bool) bool {
	if from == target {
		return true
	}
	if seen[from] {
		return false
	}
	seen[from] = true
	for _, parent := range c.edges[from].Parents {
		if c.reachable(parent, target, seen) {
			return true
		}
	}
	return false
}
func (c *Collection) RemoveChild(ctx context.Context, child id.Table) error {
	if _, ok := c.edges[child]; !ok {
		return nil
	}
	ed := c.Contents().Editor()
	if err := ed.Delete(ctx, string(id.NewInheritance(child.SchemaName(), child.TableName()))); err != nil {
		return err
	}
	m, err := ed.Flush(ctx)
	if err != nil {
		return err
	}
	return c.replaceContents(ctx, m)
}
func (c *Collection) DirectChildren(_ context.Context, parent id.Table) []id.Table {
	children := make([]id.Table, 0)
	for child, edge := range c.edges {
		for _, candidate := range edge.Parents {
			if candidate == parent {
				children = append(children, child)
				break
			}
		}
	}
	sortTables(children)
	return children
}
func (c *Collection) Descendants(ctx context.Context, parent id.Table) []id.Table {
	result := make([]id.Table, 0)
	seen := map[id.Table]bool{parent: true}
	queue := c.DirectChildren(ctx, parent)
	for len(queue) > 0 {
		child := queue[0]
		queue = queue[1:]
		if seen[child] {
			continue
		}
		seen[child] = true
		result = append(result, child)
		queue = append(queue, c.DirectChildren(ctx, child)...)
	}
	return result
}
func (c *Collection) RenameTable(ctx context.Context, oldID, newID id.Table) error {
	if !oldID.IsValid() || !newID.IsValid() {
		return errors.New("invalid table ID for inheritance rename")
	}
	if oldID == newID {
		return nil
	}
	copyEdges := make(map[id.Table][]id.Table, len(c.edges))
	for child, edge := range c.edges {
		nc := child
		if nc == oldID {
			nc = newID
		}
		parents := cloneTables(edge.Parents)
		for i := range parents {
			if parents[i] == oldID {
				parents[i] = newID
			}
		}
		copyEdges[nc] = parents
	}
	if _, collision := c.edges[newID]; collision && oldID != newID {
		return errors.Errorf("inheritance metadata already exists for %s", string(newID))
	}
	ed := c.Contents().Editor()
	for child := range c.edges {
		if err := ed.Delete(ctx, string(id.NewInheritance(child.SchemaName(), child.TableName()))); err != nil {
			return err
		}
	}
	children := make([]id.Table, 0, len(copyEdges))
	for child := range copyEdges {
		children = append(children, child)
	}
	sort.Slice(children, func(i, j int) bool { return string(children[i]) < string(children[j]) })
	for _, child := range children {
		edge := Edge{Child: child, Parents: copyEdges[child]}
		data, err := edge.Serialize(ctx)
		if err != nil {
			return err
		}
		h, err := c.NodeStore().WriteBytes(ctx, data)
		if err != nil {
			return err
		}
		if err = ed.Add(ctx, string(id.NewInheritance(child.SchemaName(), child.TableName())), h); err != nil {
			return err
		}
	}
	m, err := ed.Flush(ctx)
	if err != nil {
		return err
	}
	return c.replaceContents(ctx, m)
}

func (e Edge) GetID() id.Id {
	return id.NewInheritance(e.Child.SchemaName(), e.Child.TableName()).AsId()
}
func (e Edge) GetRootObjectID() objinterface.RootObjectID {
	return objinterface.RootObjectID_Inheritance
}
func (e Edge) HashOf(ctx context.Context) (hash.Hash, error) {
	b, err := e.Serialize(ctx)
	if err != nil {
		return hash.Hash{}, err
	}
	return hash.Of(b), nil
}
func (e Edge) Name() doltdb.TableName {
	return inheritanceObjectName(e.Child)
}

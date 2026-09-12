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
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/dolthub/dolt/go/libraries/doltcore/doltdb"

	"github.com/dolthub/doltgresql/core/id"
	"github.com/dolthub/doltgresql/core/rootobject/objinterface"
	pgtypes "github.com/dolthub/doltgresql/server/types"
)

func (c *Collection) DeserializeRootObject(ctx context.Context, data []byte) (objinterface.RootObject, error) {
	e, err := DeserializeEdge(ctx, data)
	return e, err
}
func (c *Collection) DiffRootObjects(ctx context.Context, fromHash string, ours, theirs, ancestor objinterface.RootObject) ([]objinterface.RootObjectDiff, objinterface.RootObject, error) {
	o, ok := ours.(Edge)
	if !ok {
		return nil, nil, errors.Errorf("invalid inheritance object %T", ours)
	}
	t, ok := theirs.(Edge)
	if !ok {
		return nil, nil, errors.Errorf("invalid inheritance object %T", theirs)
	}
	ob, _ := o.Serialize(ctx)
	tb, _ := t.Serialize(ctx)
	if bytes.Equal(ob, tb) {
		return nil, o, nil
	}
	if a, ok := ancestor.(Edge); ok {
		ab, _ := a.Serialize(ctx)
		if bytes.Equal(ob, ab) {
			return nil, t, nil
		}
		if bytes.Equal(tb, ab) {
			return nil, o, nil
		}
	}
	var ancestorValue any
	if a, ok := ancestor.(Edge); ok {
		ancestorValue = formatParents(a.Parents)
	}
	return []objinterface.RootObjectDiff{{Type: pgtypes.Text, FromHash: fromHash, FieldName: "parents", AncestorValue: ancestorValue, OurValue: formatParents(o.Parents), TheirValue: formatParents(t.Parents), OurChange: objinterface.RootObjectDiffChange_Modified, TheirChange: objinterface.RootObjectDiffChange_Modified}}, nil, nil
}
func (c *Collection) DropRootObject(ctx context.Context, identifier id.Id) error {
	if identifier.Section() != id.Section_Inheritance {
		return errors.New("invalid inheritance child ID")
	}
	meta := id.Inheritance(identifier)
	return c.RemoveChild(ctx, id.NewTable(meta.SchemaName(), meta.TableName()))
}
func (c *Collection) GetFieldType(_ context.Context, fieldName string) *pgtypes.DoltgresType {
	if fieldName == "parents" {
		return pgtypes.Text
	}
	return nil
}
func (c *Collection) GetID() objinterface.RootObjectID { return objinterface.RootObjectID_Inheritance }
func (c *Collection) GetRootObject(_ context.Context, identifier id.Id) (objinterface.RootObject, bool, error) {
	if identifier.Section() != id.Section_Inheritance {
		return nil, false, nil
	}
	meta := id.Inheritance(identifier)
	e, ok := c.edges[id.NewTable(meta.SchemaName(), meta.TableName())]
	return e, ok, nil
}
func (c *Collection) HasRootObject(_ context.Context, identifier id.Id) (bool, error) {
	if identifier.Section() != id.Section_Inheritance {
		return false, nil
	}
	meta := id.Inheritance(identifier)
	_, ok := c.edges[id.NewTable(meta.SchemaName(), meta.TableName())]
	return ok, nil
}
func (c *Collection) IDToTableName(identifier id.Id) doltdb.TableName {
	if identifier.Section() != id.Section_Inheritance {
		return doltdb.TableName{}
	}
	t := id.Inheritance(identifier)
	return inheritanceObjectName(id.NewTable(t.SchemaName(), t.TableName()))
}
func (c *Collection) IterAll(_ context.Context, cb func(objinterface.RootObject) (bool, error)) error {
	keys := make([]id.Table, 0, len(c.edges))
	for k := range c.edges {
		keys = append(keys, k)
	}
	sortTables(keys)
	for _, k := range keys {
		stop, err := cb(c.edges[k])
		if err != nil {
			return err
		}
		if stop {
			return nil
		}
	}
	return nil
}
func (c *Collection) IterIDs(ctx context.Context, cb func(id.Id) (bool, error)) error {
	return c.IterAll(ctx, func(o objinterface.RootObject) (bool, error) { return cb(o.GetID()) })
}
func (c *Collection) PutRootObject(ctx context.Context, rootObj objinterface.RootObject) error {
	e, ok := rootObj.(Edge)
	if !ok {
		return errors.Errorf("invalid inheritance root object %T", rootObj)
	}
	return c.SetParents(ctx, e.Child, e.Parents)
}
func (c *Collection) RenameRootObject(ctx context.Context, oldID, newID id.Id) error {
	if oldID.Section() != id.Section_Inheritance || newID.Section() != id.Section_Inheritance {
		return errors.New("invalid inheritance rename IDs")
	}
	o, n := id.Inheritance(oldID), id.Inheritance(newID)
	return c.RenameTable(ctx, id.NewTable(o.SchemaName(), o.TableName()), id.NewTable(n.SchemaName(), n.TableName()))
}
func (c *Collection) ResolveName(_ context.Context, name doltdb.TableName) (doltdb.TableName, id.Id, error) {
	for child := range c.edges {
		if inheritanceObjectName(child) == name {
			return name, id.NewInheritance(child.SchemaName(), child.TableName()).AsId(), nil
		}
	}
	return doltdb.TableName{}, id.Null, nil
}
func (c *Collection) TableNameToID(name doltdb.TableName) id.Id {
	const prefix = "doltgres_inheritance_"
	if !strings.HasPrefix(name.Name, prefix) {
		return id.Null
	}
	raw, err := hex.DecodeString(strings.TrimPrefix(name.Name, prefix))
	if err != nil {
		return id.Null
	}
	child := id.Table(raw)
	if !validTableID(child) || child.SchemaName() != name.Schema {
		return id.Null
	}
	return id.NewInheritance(child.SchemaName(), child.TableName()).AsId()
}
func (c *Collection) UpdateField(context.Context, objinterface.RootObject, string, any) (objinterface.RootObject, error) {
	return nil, errors.New("inheritance conflict fields cannot be updated")
}
func sortTables(v []id.Table) {
	sort.Slice(v, func(i, j int) bool { return string(v[i]) < string(v[j]) })
}

func inheritanceObjectName(child id.Table) doltdb.TableName {
	// Root objects must live in an existing database schema to be included by
	// Dolt's UnionTableNames merge traversal. The encoded ID keeps this internal
	// name distinct from the user table whose inheritance metadata it represents.
	return doltdb.TableName{Schema: child.SchemaName(), Name: "doltgres_inheritance_" + hex.EncodeToString([]byte(child))}
}

func formatParents(parents []id.Table) string {
	names := make([]string, len(parents))
	for i, parent := range parents {
		names[i] = parent.SchemaName() + "." + parent.TableName()
	}
	b, _ := json.Marshal(names)
	return string(b)
}

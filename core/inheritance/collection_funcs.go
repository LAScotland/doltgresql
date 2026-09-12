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

	"github.com/cockroachdb/errors"
	"github.com/dolthub/dolt/go/libraries/doltcore/doltdb"
	"github.com/dolthub/dolt/go/libraries/doltcore/merge"
	"github.com/dolthub/doltgresql/core/id"
	pgmerge "github.com/dolthub/doltgresql/core/merge"
	"github.com/dolthub/doltgresql/core/rootobject/objinterface"
	"github.com/dolthub/doltgresql/flatbuffers/gen/serial"
)

var storage = objinterface.RootObjectSerializer{Bytes: (*serial.RootValue).InheritanceBytes, RootValueAdd: serial.RootValueAddInheritance}

func (*Collection) LoadCollection(ctx context.Context, root objinterface.RootValue) (objinterface.Collection, error) {
	return LoadCollection(ctx, root)
}
func LoadCollection(ctx context.Context, root objinterface.RootValue) (*Collection, error) {
	rom, err := objinterface.NewRootObjectMap(ctx, storage, root)
	if err != nil {
		return nil, err
	}
	return NewCollection(ctx, rom)
}
func (*Collection) ResolveNameFromObjects(_ context.Context, name doltdb.TableName, objects []objinterface.RootObject) (doltdb.TableName, id.Id, error) {
	for _, o := range objects {
		if e, ok := o.(Edge); ok && inheritanceObjectName(e.Child) == name {
			return name, id.NewInheritance(e.Child.SchemaName(), e.Child.TableName()).AsId(), nil
		}
	}
	return doltdb.TableName{}, id.Null, nil
}
func (*Collection) Serializer() objinterface.RootObjectSerializer { return storage }
func (*Collection) HandleMerge(ctx context.Context, m merge.MergeRootObject) (doltdb.RootObject, *merge.MergeStats, error) {
	o, ok := m.OurRootObj.(Edge)
	if !ok {
		return nil, nil, errors.Errorf("invalid inheritance merge object %T", m.OurRootObj)
	}
	t, ok := m.TheirRootObj.(Edge)
	if !ok {
		return nil, nil, errors.Errorf("invalid inheritance merge object %T", m.TheirRootObj)
	}
	oh, _ := o.HashOf(ctx)
	th, _ := t.HashOf(ctx)
	if oh.Equal(th) {
		return o, &merge.MergeStats{Operation: merge.TableUnmodified}, nil
	}
	if a, ok := m.AncestorRootObj.(Edge); ok {
		ah, _ := a.HashOf(ctx)
		if oh.Equal(ah) {
			return t, &merge.MergeStats{Operation: merge.TableModified, Modifications: 1}, nil
		}
		if th.Equal(ah) {
			return o, &merge.MergeStats{Operation: merge.TableUnmodified}, nil
		}
	}
	return pgmerge.CreateConflict(ctx, m.RightSrc, o, t, m.AncestorRootObj)
}

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
	"github.com/dolthub/doltgresql/core/id"
	"github.com/dolthub/doltgresql/utils"
)

func (edge Edge) Serialize(context.Context) ([]byte, error) {
	if !edge.Child.IsValid() {
		return nil, nil
	}
	w := utils.NewWriter(128)
	w.VariableUint(0)
	w.Id(edge.Child.AsId())
	w.VariableUint(uint64(len(edge.Parents)))
	for _, parent := range edge.Parents {
		w.Id(parent.AsId())
	}
	return w.Data(), nil
}

func DeserializeEdge(_ context.Context, data []byte) (Edge, error) {
	if len(data) == 0 {
		return Edge{}, nil
	}
	r := utils.NewReader(data)
	if version := r.VariableUint(); version != 0 {
		return Edge{}, errors.Errorf("version %d of inheritance metadata is not supported, please upgrade the server", version)
	}
	edge := Edge{Child: id.Table(r.Id())}
	count := r.VariableUint()
	edge.Parents = make([]id.Table, count)
	for i := range edge.Parents {
		edge.Parents[i] = id.Table(r.Id())
	}
	if !r.IsEmpty() {
		return Edge{}, errors.New("extra data found while deserializing inheritance metadata")
	}
	return edge, nil
}

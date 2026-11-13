// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/reffield"
)

// ReferenceField is one field of a DAG that value resolution reads.
type ReferenceField = reffield.ReferenceField

// ReferenceFields returns the fields of dag that value resolution reads.
func ReferenceFields(dag *ir.DAG) []ReferenceField {
	return reffield.ReferenceFields(dag)
}

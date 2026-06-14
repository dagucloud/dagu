// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package core

// ValidateValueResolutionReferencesForTest exposes validateValueResolutionReferences to external tests.
func ValidateValueResolutionReferencesForTest(dag *DAG) ErrorList {
	return validateValueResolutionReferences(dag)
}

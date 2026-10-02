// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

// xlsxAction maps an xlsx.<operation> action to the xlsx executor. The
// reading operations publish fixed outputs, so output declarations on them
// are rejected.
func xlsxAction(operation string, fixedOutputs bool) actionNormalizer {
	return func(normalized map[string]any, with map[string]any) error {
		if fixedOutputs {
			if err := validateFixedOutputs(normalized, "xlsx"); err != nil {
				return err
			}
		}
		return normalizeOperationAction(normalized, "xlsx", with, operation)
	}
}

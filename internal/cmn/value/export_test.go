// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import "os/exec"

// BuildShellCommandForTest exposes shell command construction to external tests.
func BuildShellCommandForTest(shell, cmdStr string) *exec.Cmd {
	return buildShellCommand(shell, cmdStr)
}

type ModeForTest = mode
type ReferenceForTest = reference
type ReferenceKindForTest = referenceKind

const (
	ModeConstLoadForTest        = modeConstLoad
	ModeStaticValidationForTest = modeStaticValidation
	ModeWorkflowValueForTest    = modeWorkflowValue
	ModeShellCommandForTest     = modeShellCommand
	ModeDirectCommandForTest    = modeDirectCommand
	ModeDynamicEvalForTest      = modeDynamicEval

	ReferenceStrictForTest  = referenceStrict
	ReferenceEvalForTest    = referenceEval
	ReferenceInvalidForTest = referenceInvalid
)

func ScanReferencesForTest(raw string) []ReferenceForTest {
	return scanReferences(raw)
}

func ValidateReferencesForTest(raw string, staticScope StaticScope, mode ModeForTest, field string) error {
	return validateReferences(raw, staticScope, mode, field)
}

func ExpandStringForTest(raw string, runtimeScope RuntimeScope, mode ModeForTest, field string) (string, error) {
	return expandString(raw, runtimeScope, mode, field)
}

func ExpandObjectForTest[T any](obj T, runtimeScope RuntimeScope, mode ModeForTest, field string) (T, error) {
	return expandObject(obj, runtimeScope, mode, field)
}

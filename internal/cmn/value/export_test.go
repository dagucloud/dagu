// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import "os/exec"

// BuildShellCommandForTest exposes shell command construction to external tests.
func BuildShellCommandForTest(shell, cmdStr string) *exec.Cmd {
	return buildShellCommand(shell, cmdStr)
}

type SemanticFieldForTest struct {
	Name  string
	Field Field
}

func SemanticFieldsForTest(path string) []SemanticFieldForTest {
	command := CommandContext{}
	return []SemanticFieldForTest{
		{Name: "Workflow", Field: WorkflowField(path)},
		{Name: "ConstLoad", Field: ConstLoadField(path)},
		{Name: "StaticValidation", Field: StaticValidationField(path)},
		{Name: "WorkflowObject", Field: WorkflowObjectField(path)},
		{Name: "HostConfigObject", Field: HostConfigObjectField(path)},
		{Name: "DAGEnv", Field: DAGEnvField(path)},
		{Name: "RuntimeDAGEnv", Field: RuntimeDAGEnvField(path)},
		{Name: "DynamicParamEval", Field: DynamicParamEvalField(path)},
		{Name: "DotenvPath", Field: DotenvPathField(path)},
		{Name: "StepDir", Field: StepDirField(path)},
		{Name: "DAGWorkingDir", Field: DAGWorkingDirField(path)},
		{Name: "AgentWorkingDir", Field: AgentWorkingDirField(path)},
		{Name: "ServerBasePath", Field: ServerBasePathField(path)},
		{Name: "LogPath", Field: LogPathField(path)},
		{Name: "CoordinatorArtifactBaseDir", Field: CoordinatorArtifactBaseDirField(path)},
		{Name: "StepArtifactOutput", Field: StepArtifactOutputField(path)},
		{Name: "StructuredOutputPath", Field: StructuredOutputPathField(path)},
		{Name: "StructuredOutputLiteral", Field: StructuredOutputLiteralField(path)},
		{Name: "DAGShell", Field: DAGShellField(path)},
		{Name: "StepShell", Field: StepShellField(path)},
		{Name: "ConditionValue", Field: ConditionValueField(path)},
		{Name: "ConditionCommand", Field: ConditionCommandField(path, command)},
		{Name: "DirectCommand", Field: DirectCommandField(path, command)},
		{Name: "ShellCommand", Field: ShellCommandField(path, command)},
		{Name: "CommandScript", Field: CommandScriptField(path, command)},
		{Name: "Container", Field: ContainerField(path)},
		{Name: "ContainerEnv", Field: ContainerEnvField(path)},
		{Name: "ExecutorConfig", Field: ExecutorConfigField(path)},
		{Name: "TemplateScript", Field: TemplateScriptField(path)},
		{Name: "TemplateConfig", Field: TemplateConfigField(path)},
		{Name: "SubDAGName", Field: SubDAGNameField(path)},
		{Name: "SubDAGParams", Field: SubDAGParamsField(path)},
		{Name: "ParallelItem", Field: ParallelItemField(path)},
		{Name: "ParallelItemParam", Field: ParallelItemParamField(path)},
		{Name: "ParallelSubDAG", Field: ParallelSubDAGField(path)},
		{Name: "RetryInteger", Field: RetryIntegerField(path)},
		{Name: "RepeatInteger", Field: RepeatIntegerField(path)},
	}
}

func FieldKindCountForTest() int {
	return fieldKindCount
}

func FieldKindForTest(field Field) int {
	return int(field.kind)
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

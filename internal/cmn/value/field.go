// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

// Field identifies the workflow value being resolved.
type Field struct {
	path    string
	kind    fieldKind
	command CommandContext
	profile ConfigProfile
}

type fieldKind int

const (
	fieldWorkflow fieldKind = iota
	fieldConstLoad
	fieldStaticValidation
	fieldWorkflowObject
	fieldHostConfigObject
	fieldDAGEnv
	fieldRuntimeDAGEnv
	fieldDynamicParamEval
	fieldDotenvPath
	fieldStepDir
	fieldDAGWorkingDir
	fieldAgentWorkingDir
	fieldServerBasePath
	fieldLogPath
	fieldCoordinatorArtifactBaseDir
	fieldStepArtifactOutput
	fieldStructuredOutputPath
	fieldStructuredOutputLiteral
	fieldDAGShell
	fieldStepShell
	fieldConditionValue
	fieldConditionCommand
	fieldDirectCommand
	fieldShellCommand
	fieldCommandScript
	fieldContainer
	fieldContainerEnv
	fieldExecutorConfig
	fieldTemplateScript
	fieldTemplateConfig
	fieldSubDAGName
	fieldSubDAGParams
	fieldParallelItem
	fieldParallelItemParam
	fieldParallelSubDAG
	fieldRetryInteger
	fieldRepeatInteger
)

// Path returns the caller-visible field path used in validation errors.
func (f Field) Path() string {
	return f.path
}

func newField(path string, kind fieldKind) Field {
	return Field{path: path, kind: kind}
}

// ConstLoadField returns the policy for loading const declarations.
func ConstLoadField(path string) Field { return newField(path, fieldConstLoad) }

// StaticValidationField returns the policy for static DAG validation.
func StaticValidationField(path string) Field { return newField(path, fieldStaticValidation) }

// WorkflowField returns the policy for ordinary workflow scalar values.
func WorkflowField(path string) Field { return newField(path, fieldWorkflow) }

// WorkflowObjectField returns the policy for ordinary workflow object values.
func WorkflowObjectField(path string) Field { return newField(path, fieldWorkflowObject) }

// HostConfigObjectField returns the policy for host-side configuration objects.
func HostConfigObjectField(path string) Field { return newField(path, fieldHostConfigObject) }

// DAGEnvField returns the policy for build-time DAG env entries.
func DAGEnvField(path string) Field { return newField(path, fieldDAGEnv) }

// RuntimeDAGEnvField returns the policy for runtime DAG env entries.
func RuntimeDAGEnvField(path string) Field { return newField(path, fieldRuntimeDAGEnv) }

// DynamicParamEvalField returns the policy for dynamic param eval values.
func DynamicParamEvalField(path string) Field { return newField(path, fieldDynamicParamEval) }

// DotenvPathField returns the policy for dotenv path values.
func DotenvPathField(path string) Field { return newField(path, fieldDotenvPath) }

// StepDirField returns the policy for step working directory values.
func StepDirField(path string) Field { return newField(path, fieldStepDir) }

// DAGWorkingDirField returns the policy for DAG working directory values.
func DAGWorkingDirField(path string) Field { return newField(path, fieldDAGWorkingDir) }

// AgentWorkingDirField returns the policy for agent working directory values.
func AgentWorkingDirField(path string) Field { return newField(path, fieldAgentWorkingDir) }

// ServerBasePathField returns the policy for frontend server base paths.
func ServerBasePathField(path string) Field { return newField(path, fieldServerBasePath) }

// LogPathField returns the policy for log path values.
func LogPathField(path string) Field { return newField(path, fieldLogPath) }

// CoordinatorArtifactBaseDirField returns the policy for coordinator artifact roots.
func CoordinatorArtifactBaseDirField(path string) Field {
	return newField(path, fieldCoordinatorArtifactBaseDir)
}

// StepArtifactOutputField returns the policy for stdout/stderr artifact paths.
func StepArtifactOutputField(path string) Field { return newField(path, fieldStepArtifactOutput) }

// StructuredOutputPathField returns the policy for structured output source paths.
func StructuredOutputPathField(path string) Field {
	return newField(path, fieldStructuredOutputPath)
}

// StructuredOutputLiteralField returns the policy for structured literal output values.
func StructuredOutputLiteralField(path string) Field {
	return newField(path, fieldStructuredOutputLiteral)
}

// DAGShellField returns the policy for DAG shell values.
func DAGShellField(path string) Field { return newField(path, fieldDAGShell) }

// StepShellField returns the policy for step shell values.
func StepShellField(path string) Field { return newField(path, fieldStepShell) }

// ConditionValueField returns the policy for non-command condition values.
func ConditionValueField(path string) Field { return newField(path, fieldConditionValue) }

// ConditionCommandField returns the policy for command condition values.
func ConditionCommandField(path string, command CommandContext) Field {
	return Field{path: path, kind: fieldConditionCommand, command: command}
}

// CommandTarget identifies where a command is executed.
type CommandTarget int

const (
	CommandTargetLocal CommandTarget = iota
	CommandTargetSSH
	CommandTargetDocker
)

// CommandContext contains command execution facts used by semantic policies.
type CommandContext struct {
	Target          CommandTarget
	Shell           []string
	ShellConfigured bool
}

// DirectCommandField returns the policy for command args executed directly.
func DirectCommandField(path string, command CommandContext) Field {
	return Field{path: path, kind: fieldDirectCommand, command: command}
}

// ShellCommandField returns the policy for command strings executed through a shell.
func ShellCommandField(path string, command CommandContext) Field {
	return Field{path: path, kind: fieldShellCommand, command: command}
}

// CommandScriptField returns the policy for command executor scripts.
func CommandScriptField(path string, command CommandContext) Field {
	return Field{path: path, kind: fieldCommandScript, command: command}
}

// ContainerField returns the policy for container scalar values.
func ContainerField(path string) Field { return newField(path, fieldContainer) }

// ContainerEnvField returns the policy for container env entries.
func ContainerEnvField(path string) Field { return newField(path, fieldContainerEnv) }

// ConfigProfile identifies executor configuration resolution behavior.
type ConfigProfile int

const (
	ConfigProfileDefault ConfigProfile = iota
	ConfigProfileTemplate
)

// ExecutorConfigField returns the policy for executor configuration objects.
func ExecutorConfigField(path string, profile ConfigProfile) Field {
	return Field{path: path, kind: fieldExecutorConfig, profile: profile}
}

// TemplateScriptField returns the policy for template executor scripts.
func TemplateScriptField(path string) Field { return newField(path, fieldTemplateScript) }

// TemplateConfigField returns the policy for template executor configuration.
func TemplateConfigField(path string) Field {
	return Field{path: path, kind: fieldTemplateConfig, profile: ConfigProfileTemplate}
}

// SubDAGNameField returns the policy for sub-DAG names.
func SubDAGNameField(path string) Field { return newField(path, fieldSubDAGName) }

// SubDAGParamsField returns the policy for sub-DAG params.
func SubDAGParamsField(path string) Field { return newField(path, fieldSubDAGParams) }

// ParallelItemField returns the policy for parallel item lists.
func ParallelItemField(path string) Field { return newField(path, fieldParallelItem) }

// ParallelItemParamField returns the policy for parallel item params.
func ParallelItemParamField(path string) Field { return newField(path, fieldParallelItemParam) }

// ParallelSubDAGField returns the policy for parallel sub-DAG child values.
func ParallelSubDAGField(path string) Field { return newField(path, fieldParallelSubDAG) }

// RetryIntegerField returns the policy for retry integer values.
func RetryIntegerField(path string) Field { return newField(path, fieldRetryInteger) }

// RepeatIntegerField returns the policy for repeat integer values.
func RepeatIntegerField(path string) Field { return newField(path, fieldRepeatInteger) }

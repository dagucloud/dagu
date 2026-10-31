// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import "strings"

// Field identifies the workflow value being resolved.
type Field struct {
	path    string
	kind    fieldKind
	command CommandContext
	// isPath marks a value that names a filesystem path.
	isPath bool
	// pathKeys lists the dotted object keys whose values name filesystem paths.
	pathKeys []string
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
	fieldDAGEnvCompletion
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
	fieldConditionRuntimeValue
	fieldConditionEval
	fieldConditionCommand
	fieldDirectCommand
	fieldShellCommand
	fieldCommandScript
	fieldContainer
	fieldStepEnv
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

func newPathField(path string, kind fieldKind) Field {
	return newField(path, kind).asPath()
}

// asPath returns f for a value that names a filesystem path. A backslash in
// such a value is path text, never an escape marker, so C:\out\${NAME} keeps
// its separator and resolves the reference.
func (f Field) asPath() Field {
	f.isPath = true
	return f
}

// WithPathKeys returns f for an object value whose listed keys name filesystem
// paths. A key such as "import.input_file" names a value nested in a map. A
// backslash in those values is path text, never an escape marker.
func (f Field) WithPathKeys(keys ...string) Field {
	f.pathKeys = keys
	return f
}

// Key returns the field that resolves the value under key in an object field.
func (f Field) Key(key string) Field {
	child := f
	child.pathKeys = nil
	for _, pathKey := range f.pathKeys {
		if pathKey == key {
			child = child.asPath()
		} else if nested, ok := strings.CutPrefix(pathKey, key+"."); ok {
			child.pathKeys = append(child.pathKeys, nested)
		}
	}
	return child
}

// escapesDollar reports whether a backslash before $ is a Dagu escape marker.
func (f Field) escapesDollar() bool {
	return !f.isPath
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

// DAGEnvCompletionField returns the policy for completing a DAG env entry that
// loading already resolved. Escapes applied at load are not applied again.
func DAGEnvCompletionField(path string) Field { return newField(path, fieldDAGEnvCompletion) }

// DynamicParamEvalField returns the policy for dynamic param eval values.
func DynamicParamEvalField(path string) Field { return newField(path, fieldDynamicParamEval) }

// DotenvPathField returns the policy for dotenv path values.
func DotenvPathField(path string) Field { return newPathField(path, fieldDotenvPath) }

// StepDirField returns the policy for step working directory values.
func StepDirField(path string) Field { return newPathField(path, fieldStepDir) }

// DAGWorkingDirField returns the policy for DAG working directory values.
func DAGWorkingDirField(path string) Field { return newPathField(path, fieldDAGWorkingDir) }

// AgentWorkingDirField returns the policy for agent working directory values.
func AgentWorkingDirField(path string) Field { return newPathField(path, fieldAgentWorkingDir) }

// ServerBasePathField returns the policy for frontend server base paths.
func ServerBasePathField(path string) Field { return newField(path, fieldServerBasePath) }

// LogPathField returns the policy for log path values.
func LogPathField(path string) Field { return newPathField(path, fieldLogPath) }

// CoordinatorArtifactBaseDirField returns the policy for coordinator artifact roots.
func CoordinatorArtifactBaseDirField(path string) Field {
	return newPathField(path, fieldCoordinatorArtifactBaseDir)
}

// StepArtifactOutputField returns the policy for stdout/stderr artifact paths.
func StepArtifactOutputField(path string) Field { return newPathField(path, fieldStepArtifactOutput) }

// StructuredOutputPathField returns the policy for structured output source paths.
func StructuredOutputPathField(path string) Field {
	return newPathField(path, fieldStructuredOutputPath)
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

// ConditionRuntimeValueField returns the runtime policy for value-match condition text.
func ConditionRuntimeValueField(path string) Field {
	return newField(path, fieldConditionRuntimeValue)
}

// ConditionEvalField returns the runtime policy for dynamic precondition values.
func ConditionEvalField(path string) Field {
	return newField(path, fieldConditionEval)
}

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

// StepEnvField returns the policy for step env entries.
func StepEnvField(path string) Field { return newField(path, fieldStepEnv) }

// ContainerEnvField returns the policy for container env entries.
func ContainerEnvField(path string) Field { return newField(path, fieldContainerEnv) }

// ExecutorConfigField returns the policy for executor configuration objects.
func ExecutorConfigField(path string) Field { return newField(path, fieldExecutorConfig) }

// TemplateScriptField returns the policy for scripts that executors such as
// template and js interpret as written.
func TemplateScriptField(path string) Field { return newField(path, fieldTemplateScript) }

// IsTemplateScript reports whether the field is a script passed to its
// executor as written rather than value-resolved.
func (f Field) IsTemplateScript() bool { return f.kind == fieldTemplateScript }

// TemplateConfigField returns the policy for template executor configuration.
func TemplateConfigField(path string) Field {
	return newField(path, fieldTemplateConfig)
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

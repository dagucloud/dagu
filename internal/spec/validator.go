// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// Constants for validation limits.
const (
	maxStepIDLen   = 40
	maxStepNameLen = 255
)

// Regex patterns for validation.
var (
	stepIDPattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]*$`)
)

// reservedWords contains IDs that cannot be used as step IDs.
var reservedWords = map[string]bool{
	"env":     true,
	"params":  true,
	"args":    true,
	"stdout":  true,
	"stderr":  true,
	"output":  true,
	"outputs": true,
}

// ValidateSteps validates all steps in a ir.DAG, collecting all validation errors.
func ValidateSteps(dag *ir.DAG) error {
	var errs ir.ErrorList

	stepNames, stepIDs := collectNamesAndIDs(dag, &errs)
	validateNameIDConflicts(dag, stepNames, stepIDs, &errs)
	resolveStepDependencies(dag)
	resolveForeachStepDependencies(dag.Steps)
	validateDependenciesExist(dag, stepNames, &errs)
	validateApprovalRewindTargets(dag, stepNames, &errs)
	validateBuildSteps(dag, &errs)

	for _, step := range dag.Steps {
		errs = append(errs, validateStep(step)...)
		validateForeachConfig(step, stepNames, stepIDs, &errs)
	}

	if len(errs) == 0 {
		return nil
	}
	return errs
}

func validateBuildSteps(dag *ir.DAG, errs *ir.ErrorList) {
	if dag.Type == ir.TypeBuild {
		validateNoPreExecutionPathReference(errs, "working_dir", dag.WorkingDir)
		validateNoPreExecutionPathReference(errs, "shell", dag.Shell)
		validateNoPreExecutionPathReferences(errs, "shell_args", dag.ShellArgs)
		for _, condition := range dag.Preconditions {
			validateNoBuildPathCondition(errs, "preconditions", condition)
		}
	}
	for _, step := range dag.Steps {
		validateBuildStep(dag, step, errs)
	}
	if dag.Type == ir.TypeBuild {
		validateBuildRuntimeOutputReferences(dag, errs)
	}
	for _, handler := range []*ir.Step{
		dag.HandlerOn.Init,
		dag.HandlerOn.Failure,
		dag.HandlerOn.Success,
		dag.HandlerOn.Abort,
		dag.HandlerOn.Exit,
		dag.HandlerOn.Wait,
	} {
		if handler != nil {
			validateUnsupportedBuildPaths(*handler, "handler_on", "lifecycle handlers", errs)
		}
	}
}

func validateBuildStep(dag *ir.DAG, step ir.Step, errs *ir.ErrorList) {
	pathOutputs := 0
	for _, output := range step.Outputs {
		if output.Path != "" {
			pathOutputs++
			validateBuildPathExpression(errs, "outputs", output.Path)
		}
	}
	for _, input := range step.Inputs {
		validateBuildPathExpression(errs, "inputs", input.Path)
	}
	hasPaths := len(step.Inputs) > 0 || pathOutputs > 0
	if hasPaths {
		if dag.Type != ir.TypeBuild {
			*errs = append(*errs, ir.NewValidationError("type", dag.Type,
				fmt.Errorf("step %s declares build paths but ir.DAG type is not %q", step.Name, ir.TypeBuild)))
		}
		if pathOutputs > 1 {
			*errs = append(*errs, ir.NewValidationError("outputs", step.Outputs,
				fmt.Errorf("step %s declares more than one path output", step.Name)))
		}
		if dag.Container != nil || step.Container != nil {
			*errs = append(*errs, ir.NewValidationError("container", step.Name,
				fmt.Errorf("build paths require host command or shell execution")))
		}
		executorType := step.ExecutorConfig.Type
		if executorType != "" && executorType != "command" && executorType != "shell" {
			*errs = append(*errs, ir.NewValidationError("type", executorType,
				fmt.Errorf("build paths are not supported by executor %q", executorType)))
		}
		validateNoPreExecutionPathReference(errs, "working_dir", step.Dir)
		validateNoPreExecutionPathReference(errs, "stdout", step.Stdout)
		validateNoPreExecutionPathReference(errs, "stdout.artifact", step.StdoutArtifact)
		validateNoPreExecutionPathReference(errs, "stderr", step.Stderr)
		validateNoPreExecutionPathReference(errs, "stderr.artifact", step.StderrArtifact)
		validateNoPreExecutionPathReference(errs, "shell", step.Shell)
		validateNoPreExecutionPathReferences(errs, "shell_args", step.ShellArgs)
		validateNoPreExecutionPathReferences(errs, "shell_packages", step.ShellPackages)
		validateNoAttemptOutputReferences(errs, "continue_on.output", step.ContinueOn.Output)
		if pathOutputs > 0 && step.ContinueOn.MarkSuccess {
			*errs = append(*errs, ir.NewValidationError("continue_on.mark_success", true,
				fmt.Errorf("build path output step %s cannot mark a failed attempt as successful", step.Name)))
		}
		validateNoPreExecutionPathReference(errs, "signal_on_stop", step.SignalOnStop)
		validateNoPreExecutionPathReference(errs, "retry_policy.limit", step.RetryPolicy.LimitStr)
		validateNoPreExecutionPathReference(errs, "retry_policy.interval_sec", step.RetryPolicy.IntervalSecStr)
		validateNoPreExecutionPathReference(errs, "repeat_policy.limit", step.RepeatPolicy.LimitStr)
		validateNoPreExecutionPathReference(errs, "repeat_policy.interval_sec", step.RepeatPolicy.IntervalStr)
		validateNoPreExecutionPathReference(errs, "repeat_policy.max_interval_sec", step.RepeatPolicy.MaxIntervalStr)
		validateNoAttemptOutputCondition(errs, "repeat_policy.condition", step.RepeatPolicy.Condition)
		for _, condition := range step.Preconditions {
			validateNoAttemptOutputCondition(errs, "preconditions", condition)
		}
	}
	if step.Foreach != nil {
		for _, child := range step.Foreach.Steps {
			validateUnsupportedBuildPaths(child, "foreach.steps", "foreach steps", errs)
		}
	}
}

func validateBuildRuntimeOutputReferences(dag *ir.DAG, errs *ir.ErrorList) {
	producers := make([]ir.Step, 0)
	for _, step := range dag.Steps {
		if isPotentiallyReusableBuildStep(dag, step) {
			producers = append(producers, step)
		}
	}
	for _, field := range ReferenceFields(dag) {
		for _, producer := range producers {
			if !cmnvalue.HasStepRuntimeOutputReference(field.Value, producer.ID) {
				continue
			}
			*errs = append(*errs, ir.NewValidationError(field.Path, field.Value,
				fmt.Errorf("runtime outputs from reusable build step %s are unavailable on reuse; use ${steps.%s.outputs.<name>}", producer.Name, producer.ID)))
		}
	}
}

func isPotentiallyReusableBuildStep(dag *ir.DAG, step ir.Step) bool {
	if dag.Type != ir.TypeBuild || step.ID == "" || len(step.Outputs) != 1 || step.Outputs[0].Path == "" {
		return false
	}
	if step.Output != "" || len(step.StructuredOutput) > 0 || step.StdoutOutputs != nil {
		return false
	}
	if step.HumanTask != nil || step.Approval != nil || step.RepeatPolicy.RepeatMode != "" ||
		step.Parallel != nil || step.Foreach != nil || step.SubDAG != nil {
		return false
	}
	if dag.Container != nil || step.Container != nil {
		return false
	}
	executorType := step.ExecutorConfig.Type
	return executorType == "" || executorType == "command" || executorType == "shell"
}

func validateUnsupportedBuildPaths(step ir.Step, field, scope string, errs *ir.ErrorList) {
	hasPaths := len(step.Inputs) > 0
	for _, output := range step.Outputs {
		if output.Path != "" {
			hasPaths = true
			break
		}
	}
	if hasPaths {
		*errs = append(*errs, ir.NewValidationError(field, step.Name,
			fmt.Errorf("build paths are not supported in %s", scope)))
	}
	if step.Foreach != nil {
		for _, child := range step.Foreach.Steps {
			validateUnsupportedBuildPaths(child, field, scope, errs)
		}
	}
}

func validateBuildPathExpression(errs *ir.ErrorList, field, path string) {
	if containsCommandSubstitution(path) {
		*errs = append(*errs, ir.NewValidationError(field, path,
			fmt.Errorf("build paths cannot use command substitution")))
	}
	if containsStepReference(path) {
		*errs = append(*errs, ir.NewValidationError(field, path,
			fmt.Errorf("build paths must resolve before step execution")))
	}
	if containsInputPathReference(path) {
		*errs = append(*errs, ir.NewValidationError(field, path,
			fmt.Errorf("build paths must resolve before step execution")))
	}
	if containsAttemptOutputReference(path) {
		*errs = append(*errs, ir.NewValidationError(field, path,
			fmt.Errorf("path output references are available only during executor attempts")))
	}
}

func containsStepReference(value string) bool {
	return strings.Contains(value, "${steps.")
}

func containsCommandSubstitution(value string) bool {
	return strings.Contains(value, "$(") || strings.Contains(value, "`")
}

func containsAttemptOutputReference(value string) bool {
	return cmnvalue.HasReferenceToNamespace(value, "outputs")
}

func containsInputPathReference(value string) bool {
	return cmnvalue.HasReferenceToNamespace(value, "inputs")
}

func validateNoAttemptOutputReference(errs *ir.ErrorList, field, value string) {
	if !containsAttemptOutputReference(value) {
		return
	}
	*errs = append(*errs, ir.NewValidationError(field, value,
		fmt.Errorf("path output references are available only during executor attempts")))
}

func validateNoAttemptOutputReferences(errs *ir.ErrorList, field string, values []string) {
	for _, value := range values {
		validateNoAttemptOutputReference(errs, field, value)
	}
}

func validateNoPreExecutionPathReference(errs *ir.ErrorList, field, value string) {
	validateNoAttemptOutputReference(errs, field, value)
	validateNoInputPathReference(errs, field, value)
}

func validateNoPreExecutionPathReferences(errs *ir.ErrorList, field string, values []string) {
	for _, value := range values {
		validateNoPreExecutionPathReference(errs, field, value)
	}
}

func validateNoInputPathReference(errs *ir.ErrorList, field, value string) {
	if !containsInputPathReference(value) {
		return
	}
	*errs = append(*errs, ir.NewValidationError(field, value,
		fmt.Errorf("input path references are unavailable before step execution")))
}

func validateNoAttemptOutputCondition(errs *ir.ErrorList, field string, condition *ir.Condition) {
	if condition == nil {
		return
	}
	if containsAttemptOutputReference(condition.Condition) ||
		containsAttemptOutputReference(condition.Eval) ||
		containsAttemptOutputReference(condition.Expected) {
		*errs = append(*errs, ir.NewValidationError(field, condition,
			fmt.Errorf("path output references are available only during executor attempts")))
	}
}

func validateNoBuildPathCondition(errs *ir.ErrorList, field string, condition *ir.Condition) {
	if condition == nil {
		return
	}
	if containsAttemptOutputReference(condition.Condition) ||
		containsAttemptOutputReference(condition.Eval) ||
		containsAttemptOutputReference(condition.Expected) ||
		strings.Contains(condition.Condition, "${inputs.") ||
		strings.Contains(condition.Eval, "${inputs.") ||
		strings.Contains(condition.Expected, "${inputs.") {
		*errs = append(*errs, ir.NewValidationError(field, condition,
			fmt.Errorf("build path references are unavailable to ir.DAG preconditions")))
	}
}

// collectNamesAndIDs collects all step names and IDs, validating uniqueness and format.
func collectNamesAndIDs(dag *ir.DAG, errs *ir.ErrorList) (stepNames, stepIDs map[string]struct{}) {
	stepNames = make(map[string]struct{})
	stepIDs = make(map[string]struct{})

	for _, step := range dag.Steps {
		if step.Name == "" {
			*errs = append(*errs, ir.NewValidationError("steps", step, fmt.Errorf("internal error: step name not generated")))
			continue
		}

		// Reserved in every ir.DAG type, not only agents: the execution plan
		// recognises an agent by these names.
		if !dag.IsAgent() && ir.IsSynthesizedAgentStep(step.Name) {
			*errs = append(*errs, ir.NewValidationError("steps", step.Name,
				fmt.Errorf("%q is reserved by type: agent", step.Name)))
		}

		if _, exists := stepNames[step.Name]; exists {
			*errs = append(*errs, ir.NewValidationError("steps", step.Name, ir.ErrStepNameDuplicate))
		} else {
			stepNames[step.Name] = struct{}{}
		}

		if step.ID == "" {
			continue
		}

		if !isValidStepID(step.ID) {
			*errs = append(*errs, ir.NewValidationError("steps", step.ID, fmt.Errorf("invalid step ID format: must match %s (use '_' instead of '-')", stepIDPattern.String())))
		}

		if len(step.ID) > maxStepIDLen {
			*errs = append(*errs, ir.NewValidationError("steps", step.ID, ir.ErrStepIDTooLong))
		}

		if _, exists := stepIDs[step.ID]; exists {
			*errs = append(*errs, ir.NewValidationError("steps", step.ID, fmt.Errorf("duplicate step ID: %s", step.ID)))
		} else {
			stepIDs[step.ID] = struct{}{}
		}

		if isReservedWord(step.ID) {
			*errs = append(*errs, ir.NewValidationError("steps", step.ID, fmt.Errorf("step ID '%s' is a reserved word", step.ID)))
		}
	}

	return stepNames, stepIDs
}

// validateNameIDConflicts checks for conflicts between step names and IDs.
func validateNameIDConflicts(dag *ir.DAG, stepNames, stepIDs map[string]struct{}, errs *ir.ErrorList) {
	// Build a map of step name to its own ID for conflict checking
	nameToOwnID := make(map[string]string)
	for _, step := range dag.Steps {
		if step.Name != "" {
			nameToOwnID[step.Name] = step.ID
		}
	}

	for _, step := range dag.Steps {
		if step.Name == "" {
			continue
		}

		// Check that ID doesn't conflict with any step name (except its own)
		if step.ID != "" {
			if _, exists := stepNames[step.ID]; exists && step.ID != step.Name {
				*errs = append(*errs, ir.NewValidationError("steps", step.ID, fmt.Errorf("step ID '%s' conflicts with another step's name", step.ID)))
			}
		}

		// Check that name doesn't conflict with any ID (unless it's the same step)
		if _, exists := stepIDs[step.Name]; exists && nameToOwnID[step.Name] != step.Name {
			*errs = append(*errs, ir.NewValidationError("steps", step.Name, fmt.Errorf("step name '%s' conflicts with another step's ID", step.Name)))
		}
	}
}

// validateDependenciesExist checks that all dependencies reference existing steps.
func validateDependenciesExist(dag *ir.DAG, stepNames map[string]struct{}, errs *ir.ErrorList) {
	for _, step := range dag.Steps {
		for _, dep := range step.Depends {
			if _, exists := stepNames[dep]; !exists {
				*errs = append(*errs, ir.NewValidationError("depends", dep, fmt.Errorf("step %s depends on non-existent step %s", step.Name, dep)))
			}
		}
	}
}

func validateApprovalRewindTargets(dag *ir.DAG, stepNames map[string]struct{}, errs *ir.ErrorList) {
	stepByName := make(map[string]ir.Step, len(dag.Steps))
	for _, step := range dag.Steps {
		stepByName[step.Name] = step
	}

	for _, step := range dag.Steps {
		if step.Approval == nil || step.Approval.RewindTo == "" {
			continue
		}

		target := step.Approval.RewindTo
		if _, exists := stepNames[target]; !exists {
			*errs = append(*errs, ir.NewValidationError("approval.rewind_to", target,
				fmt.Errorf("step %s approval.rewind_to references non-existent step %s", step.Name, target)))
			continue
		}

		if target == step.Name {
			continue
		}

		if !isUpstreamDependency(stepByName, step.Name, target) {
			*errs = append(*errs, ir.NewValidationError("approval.rewind_to", target,
				fmt.Errorf("step %s approval.rewind_to must reference the step itself or an upstream dependency", step.Name)))
		}
	}
}

func isUpstreamDependency(stepByName map[string]ir.Step, stepName, target string) bool {
	start, ok := stepByName[stepName]
	if !ok {
		return false
	}

	queue := append([]string(nil), start.Depends...)
	visited := make(map[string]struct{}, len(queue))
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current == target {
			return true
		}
		if _, ok := visited[current]; ok {
			continue
		}
		visited[current] = struct{}{}
		if step, ok := stepByName[current]; ok {
			queue = append(queue, step.Depends...)
		}
	}

	return false
}

func validateStep(step ir.Step) ir.ErrorList {
	var errs ir.ErrorList

	if step.Name == "" {
		errs = append(errs, ir.NewValidationError("name", step.Name, ir.ErrStepNameRequired))
	}

	if len(step.Name) > maxStepNameLen {
		if step.ID != "" && step.Name == step.ID {
			errs = append(errs, ir.NewValidationError("name", step.Name,
				fmt.Errorf("step ID '%s' is used as display name but exceeds %d characters; add an explicit shorter 'name' field", step.ID, maxStepNameLen)))
		} else {
			errs = append(errs, ir.NewValidationError("name", step.Name, ir.ErrStepNameTooLong))
		}
	}

	errs = append(errs, validateParallelConfig(step)...)

	if err := validateStepWithValidator(step); err != nil {
		errs = append(errs, err)
	}

	return errs
}

func validateParallelConfig(step ir.Step) ir.ErrorList {
	if step.Parallel == nil {
		return nil
	}

	var errs ir.ErrorList

	if step.SubDAG == nil {
		errs = append(errs, ir.NewValidationError("parallel", step.Parallel, fmt.Errorf("parallel currently requires action: dag.run or dag.enqueue")))
	}

	if step.Parallel.MaxConcurrent < 1 || step.Parallel.MaxConcurrent > ir.MaxExpansionConcurrency {
		errs = append(errs, ir.NewValidationError("parallel.max_concurrent", step.Parallel.MaxConcurrent, fmt.Errorf("max_concurrent must be an integer from 1 through %d", ir.MaxExpansionConcurrency)))
	}

	if len(step.Parallel.Items) == 0 && step.Parallel.Variable == "" {
		errs = append(errs, ir.NewValidationError("parallel", step.Parallel, fmt.Errorf("parallel must have either items array or variable reference")))
	}

	return errs
}

func validateForeachConfig(step ir.Step, visibleNames, visibleIDs map[string]struct{}, errs *ir.ErrorList) {
	if step.Foreach == nil {
		return
	}

	bodyDAG := &ir.DAG{Steps: step.Foreach.Steps}
	bodyNames, bodyIDs := collectNamesAndIDs(bodyDAG, errs)
	validateNameIDConflicts(bodyDAG, bodyNames, bodyIDs, errs)
	validateForeachBodyCollisions(step, bodyDAG.Steps, visibleNames, visibleIDs, errs)
	validateForeachBodyDependencies(step, bodyDAG.Steps, bodyNames, visibleNames, visibleIDs, errs)
	validateApprovalRewindTargets(bodyDAG, bodyNames, errs)

	for _, bodyStep := range bodyDAG.Steps {
		if bodyStep.HumanTask != nil {
			*errs = append(*errs, ir.NewValidationError("foreach.steps", bodyStep.ID,
				fmt.Errorf("human.task cannot be used inside foreach.steps")))
		}
		*errs = append(*errs, validateStep(bodyStep)...)
		validateForeachConfig(bodyStep, bodyNames, bodyIDs, errs)
	}
}

func validateForeachBodyCollisions(parent ir.Step, bodySteps []ir.Step, visibleNames, visibleIDs map[string]struct{}, errs *ir.ErrorList) {
	for _, bodyStep := range bodySteps {
		validateForeachBodyIdentityCollision(parent, "name", bodyStep.Name, visibleNames, visibleIDs, errs)
		validateForeachBodyIdentityCollision(parent, "id", bodyStep.ID, visibleNames, visibleIDs, errs)
	}
}

func validateForeachBodyIdentityCollision(parent ir.Step, kind, value string, visibleNames, visibleIDs map[string]struct{}, errs *ir.ErrorList) {
	if value == "" {
		return
	}
	if _, ok := visibleNames[value]; ok {
		*errs = append(*errs, ir.NewValidationError("foreach.steps", value,
			fmt.Errorf("foreach step %s body step %s %q collides with a visible step name", parent.Name, kind, value)))
	}
	if _, ok := visibleIDs[value]; ok {
		*errs = append(*errs, ir.NewValidationError("foreach.steps", value,
			fmt.Errorf("foreach step %s body step %s %q collides with a visible step ID", parent.Name, kind, value)))
	}
}

func validateForeachBodyDependencies(parent ir.Step, bodySteps []ir.Step, bodyNames, visibleNames, visibleIDs map[string]struct{}, errs *ir.ErrorList) {
	for _, bodyStep := range bodySteps {
		for _, dep := range bodyStep.Depends {
			if _, ok := bodyNames[dep]; ok {
				continue
			}
			if _, ok := visibleNames[dep]; ok {
				*errs = append(*errs, ir.NewValidationError("foreach.steps.depends", dep,
					fmt.Errorf("foreach step %s body step %s depends on visible top-level step %s; body dependencies must stay inside foreach.steps", parent.Name, bodyStep.Name, dep)))
				continue
			}
			if _, ok := visibleIDs[dep]; ok {
				*errs = append(*errs, ir.NewValidationError("foreach.steps.depends", dep,
					fmt.Errorf("foreach step %s body step %s depends on visible top-level step ID %s; body dependencies must stay inside foreach.steps", parent.Name, bodyStep.Name, dep)))
				continue
			}
			*errs = append(*errs, ir.NewValidationError("foreach.steps.depends", dep,
				fmt.Errorf("foreach step %s body step %s depends on non-existent body step %s", parent.Name, bodyStep.Name, dep)))
		}
	}
}

func validateStepWithValidator(step ir.Step) error {
	if step.HumanTask != nil {
		return validateHumanTaskStep(step)
	}
	if err := registry.ValidateStep(step); err != nil {
		if _, ok := errors.AsType[*ir.ValidationError](err); ok {
			return err
		}
		return ir.NewValidationError("type", nil, err)
	}
	return nil
}

func validateHumanTaskStep(step ir.Step) error {
	if step.ExecutorConfig.Type != "" || len(step.ExecutorConfig.Config) > 0 || len(step.ExecutorConfig.Metadata) > 0 {
		return ir.NewValidationError("type", step.ExecutorConfig.Type, fmt.Errorf("human task cannot use an executor"))
	}
	if len(step.Commands) > 0 || step.Command != "" || step.CmdWithArgs != "" || step.CmdArgsSys != "" ||
		step.ShellCmdArgs != "" || step.Script != "" || len(step.Args) > 0 {
		return ir.NewValidationError("command", nil, fmt.Errorf("human task cannot execute commands"))
	}
	return nil
}

func isValidStepID(id string) bool {
	return stepIDPattern.MatchString(id)
}

func isReservedWord(id string) bool {
	return reservedWords[strings.ToLower(id)]
}

// resolveStepDependencies resolves step IDs to step names in the depends field.
func resolveStepDependencies(dag *ir.DAG) {
	idToName := make(map[string]string)
	for i := range dag.Steps {
		if dag.Steps[i].ID != "" {
			idToName[dag.Steps[i].ID] = dag.Steps[i].Name
		}
	}

	for i := range dag.Steps {
		for j, dep := range dag.Steps[i].Depends {
			if name, exists := idToName[dep]; exists {
				dag.Steps[i].Depends[j] = name
			}
		}
		if dag.Steps[i].Approval != nil {
			if name, exists := idToName[dag.Steps[i].Approval.RewindTo]; exists {
				dag.Steps[i].Approval.RewindTo = name
			}
		}
	}
}

func resolveForeachStepDependencies(steps []ir.Step) {
	for i := range steps {
		if steps[i].Foreach == nil {
			continue
		}
		bodyDAG := &ir.DAG{Steps: steps[i].Foreach.Steps}
		resolveStepDependencies(bodyDAG)
		steps[i].Foreach.Steps = bodyDAG.Steps
		resolveForeachStepDependencies(steps[i].Foreach.Steps)
	}
}

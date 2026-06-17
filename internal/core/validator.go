// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// Constants for validation limits.
const (
	DAGNameMaxLen  = 40
	maxStepIDLen   = 40
	maxStepNameLen = 255
)

// Regex patterns for validation.
var (
	dagNameRegex  = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)
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

// ValidateDAGName validates a DAG name according to shared rules.
// Empty name is allowed (caller may provide one via context or filename).
// Non-empty name must satisfy length and allowed character constraints.
func ValidateDAGName(name string) error {
	if name == "" {
		return nil
	}
	if name == "." || name == ".." {
		return ErrNameInvalidChars
	}
	if len(name) > DAGNameMaxLen {
		return ErrNameTooLong
	}
	if !dagNameRegex.MatchString(name) {
		return ErrNameInvalidChars
	}
	return nil
}

// StepValidator is a function type for validating step configurations.
type StepValidator func(step Step) error

// stepValidators holds registered validators for each executor type.
var stepValidators = make(map[string]StepValidator)

var stepValidatorsMu sync.RWMutex

// RegisterStepValidator registers a validator for a specific executor type.
func RegisterStepValidator(executorType string, validator StepValidator) {
	stepValidatorsMu.Lock()
	defer stepValidatorsMu.Unlock()
	stepValidators[executorType] = validator
}

// UnregisterStepValidator removes a validator for a specific executor type.
func UnregisterStepValidator(executorType string) {
	stepValidatorsMu.Lock()
	defer stepValidatorsMu.Unlock()
	delete(stepValidators, executorType)
}

// ValidateSteps validates all steps in a DAG, collecting all validation errors.
func ValidateSteps(dag *DAG) error {
	var errs ErrorList

	stepNames, stepIDs := collectNamesAndIDs(dag, &errs)
	validateNameIDConflicts(dag, stepNames, stepIDs, &errs)
	resolveStepDependencies(dag)
	validateDependenciesExist(dag, stepNames, &errs)
	validateApprovalRewindTargets(dag, stepNames, &errs)

	for _, step := range dag.Steps {
		errs = append(errs, validateStep(step)...)
	}

	if len(errs) == 0 {
		return nil
	}
	return errs
}

// collectNamesAndIDs collects all step names and IDs, validating uniqueness and format.
func collectNamesAndIDs(dag *DAG, errs *ErrorList) (stepNames, stepIDs map[string]struct{}) {
	stepNames = make(map[string]struct{})
	stepIDs = make(map[string]struct{})

	for _, step := range dag.Steps {
		if step.Name == "" {
			*errs = append(*errs, NewValidationError("steps", step, fmt.Errorf("internal error: step name not generated")))
			continue
		}

		if _, exists := stepNames[step.Name]; exists {
			*errs = append(*errs, NewValidationError("steps", step.Name, ErrStepNameDuplicate))
		} else {
			stepNames[step.Name] = struct{}{}
		}

		if step.ID == "" {
			continue
		}

		if !isValidStepID(step.ID) {
			*errs = append(*errs, NewValidationError("steps", step.ID, fmt.Errorf("invalid step ID format: must match %s (use '_' instead of '-')", stepIDPattern.String())))
		}

		if len(step.ID) > maxStepIDLen {
			*errs = append(*errs, NewValidationError("steps", step.ID, ErrStepIDTooLong))
		}

		if _, exists := stepIDs[step.ID]; exists {
			*errs = append(*errs, NewValidationError("steps", step.ID, fmt.Errorf("duplicate step ID: %s", step.ID)))
		} else {
			stepIDs[step.ID] = struct{}{}
		}

		if isReservedWord(step.ID) {
			*errs = append(*errs, NewValidationError("steps", step.ID, fmt.Errorf("step ID '%s' is a reserved word", step.ID)))
		}
	}

	return stepNames, stepIDs
}

// validateNameIDConflicts checks for conflicts between step names and IDs.
func validateNameIDConflicts(dag *DAG, stepNames, stepIDs map[string]struct{}, errs *ErrorList) {
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
				*errs = append(*errs, NewValidationError("steps", step.ID, fmt.Errorf("step ID '%s' conflicts with another step's name", step.ID)))
			}
		}

		// Check that name doesn't conflict with any ID (unless it's the same step)
		if _, exists := stepIDs[step.Name]; exists && nameToOwnID[step.Name] != step.Name {
			*errs = append(*errs, NewValidationError("steps", step.Name, fmt.Errorf("step name '%s' conflicts with another step's ID", step.Name)))
		}
	}
}

// validateDependenciesExist checks that all dependencies reference existing steps.
func validateDependenciesExist(dag *DAG, stepNames map[string]struct{}, errs *ErrorList) {
	for _, step := range dag.Steps {
		for _, dep := range step.Depends {
			if _, exists := stepNames[dep]; !exists {
				*errs = append(*errs, NewValidationError("depends", dep, fmt.Errorf("step %s depends on non-existent step %s", step.Name, dep)))
			}
		}
	}
}

func validateApprovalRewindTargets(dag *DAG, stepNames map[string]struct{}, errs *ErrorList) {
	stepByName := make(map[string]Step, len(dag.Steps))
	for _, step := range dag.Steps {
		stepByName[step.Name] = step
	}

	for _, step := range dag.Steps {
		if step.Approval == nil || step.Approval.RewindTo == "" {
			continue
		}

		target := step.Approval.RewindTo
		if _, exists := stepNames[target]; !exists {
			*errs = append(*errs, NewValidationError("approval.rewind_to", target,
				fmt.Errorf("step %s approval.rewind_to references non-existent step %s", step.Name, target)))
			continue
		}

		if target == step.Name {
			continue
		}

		if !isUpstreamDependency(stepByName, step.Name, target) {
			*errs = append(*errs, NewValidationError("approval.rewind_to", target,
				fmt.Errorf("step %s approval.rewind_to must reference the step itself or an upstream dependency", step.Name)))
		}
	}
}

func isUpstreamDependency(stepByName map[string]Step, stepName, target string) bool {
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

func validateStep(step Step) ErrorList {
	var errs ErrorList

	if step.Name == "" {
		errs = append(errs, NewValidationError("name", step.Name, ErrStepNameRequired))
	}

	if len(step.Name) > maxStepNameLen {
		if step.ID != "" && step.Name == step.ID {
			errs = append(errs, NewValidationError("name", step.Name,
				fmt.Errorf("step ID '%s' is used as display name but exceeds %d characters; add an explicit shorter 'name' field", step.ID, maxStepNameLen)))
		} else {
			errs = append(errs, NewValidationError("name", step.Name, ErrStepNameTooLong))
		}
	}

	errs = append(errs, validateParallelConfig(step)...)

	if err := validateStepWithValidator(step); err != nil {
		errs = append(errs, err)
	}

	return errs
}

func validateParallelConfig(step Step) ErrorList {
	if step.Parallel == nil {
		return nil
	}

	var errs ErrorList

	if step.SubDAG == nil {
		errs = append(errs, NewValidationError("parallel", step.Parallel, fmt.Errorf("parallel currently requires action: dag.run or dag.enqueue")))
	}

	if step.Parallel.MaxConcurrent <= 0 {
		errs = append(errs, NewValidationError("parallel.max_concurrent", step.Parallel.MaxConcurrent, fmt.Errorf("max_concurrent must be greater than 0")))
	}

	if len(step.Parallel.Items) == 0 && step.Parallel.Variable == "" {
		errs = append(errs, NewValidationError("parallel", step.Parallel, fmt.Errorf("parallel must have either items array or variable reference")))
	}

	return errs
}

func validateStepWithValidator(step Step) error {
	validator := stepValidator(step.ExecutorConfig.Type)
	if validator == nil {
		return nil
	}
	if err := validator(step); err != nil {
		var ve *ValidationError
		if errors.As(err, &ve) {
			return err
		}
		return NewValidationError("type", nil, err)
	}
	return nil
}

func stepValidator(executorType string) StepValidator {
	stepValidatorsMu.RLock()
	defer stepValidatorsMu.RUnlock()
	return stepValidators[executorType]
}

func isValidStepID(id string) bool {
	return stepIDPattern.MatchString(id)
}

func isReservedWord(id string) bool {
	return reservedWords[strings.ToLower(id)]
}

// resolveStepDependencies resolves step IDs to step names in the depends field.
func resolveStepDependencies(dag *DAG) {
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

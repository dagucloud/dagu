// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// Spec 092, "Secrets of a step" and "Untrusted steps". The step keys secrets
// and untrusted. buildStepTrust checks the rules that need only the step;
// validateStepTrust checks the rules that need the whole DAG. setEffectiveTrust
// is the one place that gives the effective value of untrusted to each step.

// stepTrustKeys are the keys that a foreach body step and the block defaults
// cannot have.
var stepTrustKeys = []string{"secrets", "untrusted"}

// buildStepTrust sets the keys secrets and untrusted of the built step.
func buildStepTrust(s *step, result *ir.Step) ir.ErrorList {
	var errs ir.ErrorList
	if s.Secrets != nil {
		result.Secrets = slices.Clone(s.Secrets)
	}

	result.UntrustedKey = s.Untrusted
	implied := result.IsUntrusted()
	if s.Untrusted == nil {
		result.Untrusted = implied
		return errs
	}
	switch {
	case result.Container != nil:
		errs = append(errs, ir.NewValidationError("untrusted", *s.Untrusted,
			fmt.Errorf("untrusted is not valid on a step with container")))
	case result.ExecutorConfig.Type == "container":
		errs = append(errs, ir.NewValidationError("untrusted", *s.Untrusted,
			fmt.Errorf("untrusted is not valid on a step that runs in the container of the DAG")))
	case implied && !*s.Untrusted:
		errs = append(errs, ir.NewValidationError("untrusted", false,
			fmt.Errorf("an agent.run step with a CLI provider is always untrusted; remove untrusted: false")))
	case !implied && !runsLocalProcess(result):
		errs = append(errs, ir.NewValidationError("untrusted", *s.Untrusted,
			fmt.Errorf("untrusted is valid only on a step with run, command or script, or with the action harness.run")))
	}
	result.Untrusted = *s.Untrusted || implied
	return errs
}

// setEffectiveTrust gives each step its effective value of Untrusted (Spec 092,
// "Default of the key"). In a DAG that has an agent.run step, a step that runs a
// process is untrusted, unless it has untrusted: false. Secrets do not change
// the default. In another DAG, the key decides alone. The function clears
// UntrustedKey, so the built DAG holds only the effective value.
func setEffectiveTrust(dag *ir.DAG) {
	agentDAG := len(dag.AgentProviders()) > 0
	dag.WalkSteps(func(step *ir.Step) {
		if agentDAG && step.UntrustedKey == nil && runsLocalProcess(step) {
			step.Untrusted = true
		}
		step.UntrustedKey = nil
	})
}

// errSecretsUntrustedByDefault is the error for a step that lists secrets and
// has no key untrusted, in a DAG that has an agent.run step.
const errSecretsUntrustedByDefault = "step lists secrets but is untrusted by default in a DAG that has an agent.run step; " +
	"add untrusted: false to run it as the process user, or remove the secrets"

// stepsWithSecretsAndDefaultTrust returns the steps that list secrets, run a
// process and have no key untrusted, in a DAG that has an agent.run step. Such
// a step is untrusted by default and cannot have secrets (Spec 092, "Errors").
func stepsWithSecretsAndDefaultTrust(dag *ir.DAG) map[*ir.Step]struct{} {
	if len(dag.AgentProviders()) == 0 {
		return nil
	}
	found := make(map[*ir.Step]struct{})
	for _, ts := range trustSteps(dag) {
		if ts.step.Secrets != nil && ts.step.UntrustedKey == nil && runsLocalProcess(ts.step) {
			found[ts.step] = struct{}{}
		}
	}
	return found
}

// runsLocalProcess reports whether the step runs a process of the host: the
// command executor with a command or a script, or harness.run.
func runsLocalProcess(step *ir.Step) bool {
	if step.HumanTask != nil || step.SubDAG != nil || step.Parallel != nil ||
		step.Foreach != nil || step.Router != nil || step.Container != nil {
		return false
	}
	switch step.ExecutorConfig.Type {
	case "", "command", "shell":
		return len(step.Commands) > 0 || step.Script != ""
	case "harness":
		return step.Agent == nil
	default:
		return false
	}
}

// rejectStepTrustKeys returns an error when raw, the block at field, has a key
// of stepTrustKeys. where names the place for the error, and hint ends it when
// it is not empty.
func rejectStepTrustKeys(field string, raw map[string]any, where, hint string) error {
	for _, key := range stepTrustKeys {
		if value, ok := raw[key]; ok {
			message := fmt.Sprintf("the key %q is not valid in %s", key, where)
			if hint != "" {
				message += "; " + hint
			}
			return ir.NewValidationError(field+"."+key, value, errors.New(message))
		}
	}
	return nil
}

// trustStep is a step of the DAG with the path of its spec field.
type trustStep struct {
	path string
	step *ir.Step
}

// trustSteps returns the steps and the handlers of the DAG.
func trustSteps(dag *ir.DAG) []trustStep {
	steps := make([]trustStep, 0, len(dag.Steps)+6)
	for i := range dag.Steps {
		steps = append(steps, trustStep{path: fmt.Sprintf("steps[%d]", i), step: &dag.Steps[i]})
	}
	for _, handler := range []struct {
		name string
		step *ir.Step
	}{
		{"init", dag.HandlerOn.Init},
		{"success", dag.HandlerOn.Success},
		{"failure", dag.HandlerOn.Failure},
		{"abort", dag.HandlerOn.Abort},
		{"exit", dag.HandlerOn.Exit},
		{"wait", dag.HandlerOn.Wait},
	} {
		if handler.step != nil {
			steps = append(steps, trustStep{path: "handler_on." + handler.name, step: handler.step})
		}
	}
	return steps
}

// stepsByPath maps the spec path of each step, and of each step in the body of a
// foreach step, to the step. The paths are the owner paths of ReferenceFields.
func stepsByPath(steps []trustStep) map[string]*ir.Step {
	byPath := make(map[string]*ir.Step, len(steps))
	for _, ts := range steps {
		byPath[ts.path] = ts.step
		if ts.step.Foreach == nil {
			continue
		}
		for i := range ts.step.Foreach.Steps {
			byPath[fmt.Sprintf("%s.foreach.steps[%d]", ts.path, i)] = &ts.step.Foreach.Steps[i]
		}
	}
	return byPath
}

// validateStepTrust checks the rules of Spec 092 that need the whole DAG and
// sets dag.SecretStepScope.
func validateStepTrust(dag *ir.DAG) ir.ErrorList {
	var errs ir.ErrorList
	// This check needs the key before setEffectiveTrust clears it.
	secretsDefault := stepsWithSecretsAndDefaultTrust(dag)
	for _, ts := range trustSteps(dag) {
		if _, ok := secretsDefault[ts.step]; ok {
			errs = append(errs, ir.NewValidationError(ts.path+".secrets", ts.step.Secrets,
				errors.New(errSecretsUntrustedByDefault)))
		}
	}
	setEffectiveTrust(dag)
	declared := make(map[string]struct{}, len(dag.Secrets))
	for _, secret := range dag.Secrets {
		declared[secret.Name] = struct{}{}
	}

	steps := trustSteps(dag)
	for _, ts := range steps {
		step := ts.step
		// A step that the DAG container runs already failed buildStepTrust.
		if step.IsUntrusted() && dag.Container != nil && step.ExecutorConfig.Type != "container" {
			errs = append(errs, ir.NewValidationError(ts.path+".untrusted", step.Name,
				fmt.Errorf("untrusted step %q cannot run in the container of the DAG", step.Name)))
		}
		if step.Secrets == nil {
			continue
		}
		if _, ok := secretsDefault[step]; ok {
			continue
		}
		if step.IsUntrusted() {
			errs = append(errs, ir.NewValidationError(ts.path+".secrets", step.Secrets,
				fmt.Errorf("untrusted step %q cannot have the key secrets", step.Name)))
			continue
		}
		seen := make(map[string]struct{}, len(step.Secrets))
		for _, name := range step.Secrets {
			if _, dup := seen[name]; dup {
				errs = append(errs, ir.NewValidationError(ts.path+".secrets", name,
					fmt.Errorf("step %q: the secret %q occurs two times in the list secrets", step.Name, name)))
				continue
			}
			seen[name] = struct{}{}
			if _, ok := declared[name]; !ok {
				errs = append(errs, ir.NewValidationError(ts.path+".secrets", name,
					fmt.Errorf("step %q: the list secrets names %q, which the secrets block of the DAG does not declare", step.Name, name)))
			}
		}
	}

	errs = append(errs, validateUntrustedOutputReferences(dag, steps)...)

	// An empty list also makes the step scope; JSON keeps only this flag of it.
	dag.SecretStepScope = dag.HasSecretStepScope()
	for _, ts := range steps {
		if ts.step.Secrets != nil {
			dag.SecretStepScope = true
		}
	}
	if !dag.SecretStepScope || len(declared) == 0 {
		return errs
	}
	errs = append(errs, validateSecretReferences(dag, steps, declared)...)
	return errs
}

// commandFieldSuffixes are the fields of a step that Dagu gives to a shell or
// runs as a command: run, command, script, and the shell with its arguments.
var commandFieldSuffixes = []string{".run", ".command", ".cmd_with_args", ".cmd_args_sys", ".shell_cmd_args", ".args[", ".shell", ".shell_args["}

// isCommandField reports whether the field of path, below the step of
// stepPath, is a command field.
func isCommandField(stepPath, path string) bool {
	rest, ok := strings.CutPrefix(path, stepPath)
	if !ok {
		return false
	}
	for _, suffix := range commandFieldSuffixes {
		if rest == suffix || strings.HasPrefix(rest, suffix+"[") || strings.HasPrefix(rest, suffix+".") ||
			(strings.HasSuffix(suffix, "[") && strings.HasPrefix(rest, suffix)) {
			return true
		}
	}
	return false
}

// untrustedSources returns the IDs and the names of the steps whose outputs are
// data of an agent: each untrusted step, and each foreach step with an
// untrusted step in its body.
func untrustedSources(steps []trustStep) map[string]string {
	sources := make(map[string]string)
	add := func(step *ir.Step) {
		if step.ID != "" {
			sources[step.ID] = step.Name
		}
		if step.Name != "" {
			sources[step.Name] = step.Name
		}
	}
	for _, ts := range steps {
		step := ts.step
		if step.IsUntrusted() {
			add(step)
			continue
		}
		if step.Foreach != nil {
			for _, body := range step.Foreach.Steps {
				if body.IsUntrusted() {
					add(step)
					break
				}
			}
		}
	}
	return sources
}

// validateUntrustedOutputReferences rejects a reference to an output of an
// untrusted step in the run, command, script or shell of a trusted step (Spec 092,
// "Trusted steps after an untrusted step"). Dagu puts the value of the
// reference in the text of the command, so the value of an agent would be
// shell code of a trusted step. The env of the step, its preconditions, its
// repeat_policy and the with of an action can have the reference.
func validateUntrustedOutputReferences(dag *ir.DAG, steps []trustStep) ir.ErrorList {
	sources := untrustedSources(steps)
	if len(sources) == 0 {
		return nil
	}
	byPath := stepsByPath(steps)
	var errs ir.ErrorList
	reported := make(map[string]struct{})
	for _, field := range ReferenceFields(dag) {
		if field.OwnerStepPath == "" || !isCommandField(field.OwnerStepPath, field.Path) {
			continue
		}
		if owner, ok := byPath[field.OwnerStepPath]; ok && owner.IsUntrusted() {
			continue
		}
		for _, ref := range cmnvalue.StepValueReferences(field.Value) {
			source, untrusted := sources[ref.Step]
			if !untrusted || source == field.OwnerStepName {
				continue
			}
			key := field.OwnerStepPath + "\x00" + ref.Text
			if _, done := reported[key]; done {
				continue
			}
			reported[key] = struct{}{}
			errs = append(errs, ir.NewValidationError(field.noticeFieldPath(), field.Value,
				fmt.Errorf("step %q: the field %s refers to %s, an output of the untrusted step %q; Dagu would put the value of the agent in the command: pass it through env and use \"$VAR\" in the command",
					field.OwnerStepName, field.noticeFieldPath(), ref.Text, source)))
		}
	}
	return errs
}

// validateSecretReferences rejects a reference ${NAME} to a secret in a field of
// a step that does not get the secret. A step in the body of a foreach step has
// no list, so it gets no secret.
func validateSecretReferences(dag *ir.DAG, steps []trustStep, declared map[string]struct{}) ir.ErrorList {
	var errs ir.ErrorList
	byPath := stepsByPath(steps)
	reported := make(map[string]struct{})
	for _, field := range ReferenceFields(dag) {
		if field.OwnerStepPath == "" {
			continue
		}
		scope := ir.StepSecretScope{Scoped: true}
		if owner, ok := byPath[field.OwnerStepPath]; ok {
			scope = dag.SecretScopeOf(*owner)
		}
		for _, name := range cmnvalue.EnvReferenceNames(field.Value) {
			if _, isSecret := declared[name]; !isSecret || scope.Allows(name) {
				continue
			}
			key := field.OwnerStepPath + "\x00" + name
			if _, done := reported[key]; done {
				continue
			}
			reported[key] = struct{}{}
			errs = append(errs, ir.NewValidationError(field.Path, field.Value,
				fmt.Errorf("step %q: the field %s refers to the secret %q, which the step does not get; add it to the list secrets of the step",
					field.OwnerStepName, field.Path, name)))
		}
	}
	return errs
}

// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intake

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/goccy/go-yaml"
)

// StepsToRun returns, in the order of dag, the steps a run of dag must run
// when it reuses source, a finished run of an earlier definition of the DAG,
// sourceDAG. A step is reused, and left out, when its definition, the DAG's
// definition other than its steps, the run's parameters, and the steps it
// depends on are as they were in source, and it succeeded there or carried
// outputs into it. Every other step runs, and so does every step after one
// that runs, so it reads what that step does now, and every step that shares
// a browser with one that runs, since each starts on the page the one before
// it left. dag has its run's
// parameters resolved, which a run's status records as source.Params does,
// and profile is the runtime profile the run uses, which can change the
// environment of every step.
func StepsToRun(dag *ir.DAG, profile string, sourceDAG *ir.DAG, source *ir.DAGRunStatus) ([]string, error) {
	if source == nil || sourceDAG == nil {
		return nil, errors.New("no run to reuse is given")
	}
	current, err := stepFingerprints(dag, strings.Join(dag.Params, " "), profile)
	if err != nil {
		return nil, err
	}
	earlier, err := stepFingerprints(sourceDAG, source.Params, source.ProfileName)
	if err != nil {
		return nil, fmt.Errorf("the run to reuse: %w", err)
	}
	nodes := make(map[string]*ir.Node, len(source.Nodes))
	for _, node := range source.Nodes {
		if node != nil {
			nodes[node.Step.Name] = node
		}
	}
	runs := make(map[string]bool, len(dag.Steps))
	// sharedRuns names the shared browsers a step that runs uses.
	sharedRuns := make(map[string]bool)
	run := func(step *ir.Step) {
		runs[step.Name] = true
		if share := browserShare(step); share != "" {
			sharedRuns[share] = true
		}
	}
	for i := range dag.Steps {
		step := &dag.Steps[i]
		node := nodes[step.Name]
		if current[step.Name] != earlier[step.Name] || node == nil || !carriesOutputs(node) {
			run(step)
		}
	}
	// A step after one that runs runs too, and so does one sharing its
	// browser.
	for changed := true; changed; {
		changed = false
		for i := range dag.Steps {
			step := &dag.Steps[i]
			if runs[step.Name] {
				continue
			}
			if slices.ContainsFunc(step.AllDepends(), func(dep string) bool { return runs[dep] }) || sharedRuns[browserShare(step)] {
				run(step)
				changed = true
			}
		}
	}
	var names []string
	for _, step := range dag.Steps {
		if runs[step.Name] {
			names = append(names, step.Name)
		}
	}
	return names, nil
}

// CheckRunStepsUnchanged makes sure that each step of fromRuns is as it was in
// its run, as StepsToRun compares a step: its definition, the DAG's definition
// other than its steps, the base config, the run's parameters and runtime
// profile, and the steps it depends on. dag has its run's parameters resolved,
// profile is the runtime profile of the run, and definitions holds the
// definition that each run of fromRuns ran. The error names the step and the
// run.
func CheckRunStepsUnchanged(dag *ir.DAG, profile string, fromRuns RunStepSources, definitions RunDefinitions) error {
	if len(fromRuns) == 0 {
		return nil
	}
	params := strings.Join(dag.Params, " ")
	current, err := stepFingerprints(dag, params, profile)
	if err != nil {
		return err
	}
	earlier := make(map[string]map[string]string, len(definitions))
	for _, ref := range slices.Sorted(maps.Keys(fromRuns)) {
		run := fromRuns[ref]
		name, err := resolveStepName(dag, ref)
		if err != nil {
			return err
		}
		definition := definitions[run.DAGRunID]
		if definition == nil {
			return fmt.Errorf("step %s cannot be reused from run %s: the definition of the run is not known", ref, run.DAGRunID)
		}
		digests, ok := earlier[run.DAGRunID]
		if !ok {
			if digests, err = stepFingerprints(definition, run.Params, run.ProfileName); err != nil {
				return fmt.Errorf("step %s cannot be reused from run %s: %w", ref, run.DAGRunID, err)
			}
			earlier[run.DAGRunID] = digests
		}
		if digests[name] == current[name] {
			continue
		}
		reason, err := changeReason(dag, params, profile, definition, run)
		if err != nil {
			return err
		}
		return fmt.Errorf("step %s cannot be reused from run %s: %s", ref, run.DAGRunID, reason)
	}
	return nil
}

// changeReason says what changed for a step whose digest differs from the one
// in run, which ran earlier: what every step shares, or the step and the steps
// it depends on.
func changeReason(dag *ir.DAG, params, profile string, earlier *ir.DAG, run *ir.DAGRunStatus) (string, error) {
	now, err := definitionDigest(dag, params, profile)
	if err != nil {
		return "", err
	}
	then, err := definitionDigest(earlier, run.Params, run.ProfileName)
	if err != nil {
		return "", err
	}
	if now != then {
		return "the parameters, runtime profile, base config, or top-level settings differ from that run", nil
	}
	return "the step or a step it depends on changed since that run", nil
}

// stepFingerprints returns, by step name, a digest of what decides what each
// step of dag does: its definition, the DAG's definition other than its
// steps, the base config, the run's parameters and runtime profile, and the
// digests of the steps it depends on.
func stepFingerprints(dag *ir.DAG, params, profile string) (map[string]string, error) {
	base, err := definitionDigest(dag, params, profile)
	if err != nil {
		return nil, err
	}
	steps := make(map[string]*ir.Step, len(dag.Steps))
	for i := range dag.Steps {
		steps[dag.Steps[i].Name] = &dag.Steps[i]
	}
	digests := make(map[string]string, len(dag.Steps))
	visiting := make(map[string]bool, len(dag.Steps))
	var digest func(name string) (string, error)
	digest = func(name string) (string, error) {
		if d, ok := digests[name]; ok {
			return d, nil
		}
		step, ok := steps[name]
		if !ok {
			return "", fmt.Errorf("step %q depends on a step the DAG does not have", name)
		}
		if visiting[name] {
			return "", fmt.Errorf("step %q depends on itself", name)
		}
		visiting[name] = true
		definition, err := stepDefinition(step)
		if err != nil {
			return "", err
		}
		hash := sha256.New()
		_, _ = fmt.Fprintf(hash, "%s\x00%s", base, definition)
		deps := slices.Sorted(slices.Values(step.AllDepends()))
		for _, dep := range deps {
			d, err := digest(dep)
			if err != nil {
				return "", err
			}
			_, _ = fmt.Fprintf(hash, "\x00%s=%s", dep, d)
		}
		digests[name] = hex.EncodeToString(hash.Sum(nil))
		return digests[name], nil
	}
	for name := range steps {
		if _, err := digest(name); err != nil {
			return nil, err
		}
	}
	return digests, nil
}

// definitionDigest is a digest of the DAG's definition other than its steps,
// as written, with the base config and the run's parameters and runtime
// profile. The definition as written holds what its loaded form leaves out,
// such as the environment.
func definitionDigest(dag *ir.DAG, params, profile string) (string, error) {
	if len(dag.YamlData) == 0 {
		return "", errors.New("the DAG keeps no definition to compare")
	}
	var document map[string]any
	if err := yaml.Unmarshal(dag.YamlData, &document); err != nil {
		return "", fmt.Errorf("read the DAG's definition: %w", err)
	}
	delete(document, "steps")
	data, err := json.Marshal(document)
	if err != nil {
		return "", fmt.Errorf("read the DAG's definition: %w", err)
	}
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "%s\x00%s\x00%s\x00%s", data, dag.BaseConfigData, params, profile)
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// stepDefinition is a step's loaded definition as a run keeps it, so a step
// read back from an earlier run compares equal to the same step loaded now.
func stepDefinition(step *ir.Step) ([]byte, error) {
	data, err := json.Marshal(step)
	if err != nil {
		return nil, fmt.Errorf("read step %q: %w", step.Name, err)
	}
	var kept ir.Step
	if err := json.Unmarshal(data, &kept); err != nil {
		return nil, fmt.Errorf("read step %q: %w", step.Name, err)
	}
	return json.Marshal(kept)
}

// browserShare is the name of the browser a step shares with other steps of
// its run, or empty when it shares none.
func browserShare(step *ir.Step) string {
	if step.ExecutorConfig.Type != ir.ExecutorTypeBrowser {
		return ""
	}
	browser, _ := step.ExecutorConfig.Config["browser"].(map[string]any)
	share, _ := browser["share"].(string)
	return share
}

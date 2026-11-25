// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intake

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
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
	current, err := newFingerprint(dag, strings.Join(dag.Params, " "), profile)
	if err != nil {
		return nil, err
	}
	earlier, err := newFingerprint(sourceDAG, source.Params, source.ProfileName)
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
	tainted := taintedSteps(dag, source)
	for i := range dag.Steps {
		step := &dag.Steps[i]
		node := nodes[step.Name]
		if len(current.changes(earlier, step.Name)) > 0 || tainted[step.Name] || node == nil || !carriesOutputs(node) {
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

// changeNouns names each change that a definition can show, for a reason.
var changeNouns = map[ir.StepChange]string{
	ir.StepChangeStep:       "the step",
	ir.StepChangeDependency: "a step it depends on",
	ir.StepChangeParams:     "the parameters",
	ir.StepChangeProfile:    "the runtime profile",
	ir.StepChangeBaseConfig: "the base config",
	ir.StepChangeSettings:   "the top-level settings",
}

// changeReason says what changed for a step since a run, such as "the step
// and the parameters (MAX) changed since that run". params names the
// parameters whose values changed, when it knows them.
func changeReason(changes []ir.StepChange, params []string) string {
	nouns := make([]string, 0, len(changes))
	var parts []string
	for _, change := range changes {
		noun, ok := changeNouns[change]
		if !ok {
			continue
		}
		if change == ir.StepChangeParams && len(params) > 0 {
			noun += " (" + strings.Join(params, ", ") + ")"
		}
		nouns = append(nouns, noun)
	}
	if len(nouns) > 0 {
		parts = append(parts, joinWords(nouns)+" changed since that run")
	}
	if slices.Contains(changes, ir.StepChangeCarried) {
		parts = append(parts, "in that run it or a step it depends on took outputs of a changed step")
	}
	if slices.Contains(changes, ir.StepChangeUnknown) {
		parts = append(parts, "what that run ran cannot be compared")
	}
	return strings.Join(parts, ", and ")
}

// changedParams names the parameters whose values differ between now and
// earlier, two lists of resolved parameters as NAME=value or a positional
// value, which is named $1, $2, and so on: in the order of now, then the
// names that only earlier has.
func changedParams(now, earlier []string) []string {
	byName := func(params []string) ([]string, map[string]string) {
		names := make([]string, 0, len(params))
		values := make(map[string]string, len(params))
		position := 0
		for _, param := range params {
			name, value, named := strings.Cut(param, "=")
			if !named || name == "" {
				position++
				name, value = fmt.Sprintf("$%d", position), param
			}
			if _, seen := values[name]; !seen {
				names = append(names, name)
			}
			values[name] = value
		}
		return names, values
	}
	nowNames, nowValues := byName(now)
	earlierNames, earlierValues := byName(earlier)
	var changed []string
	for _, name := range nowNames {
		if value, ok := earlierValues[name]; !ok || value != nowValues[name] {
			changed = append(changed, name)
		}
	}
	for _, name := range earlierNames {
		if _, ok := nowValues[name]; !ok {
			changed = append(changed, name)
		}
	}
	return changed
}

// taintedSteps returns, by name, the steps of dag that took outputs of a
// changed step in run, with consent, and every step that depends on one of
// them, directly or not.
func taintedSteps(dag *ir.DAG, run *ir.DAGRunStatus) map[string]bool {
	changed := make(map[string]bool)
	for _, input := range run.ReusedInputs {
		if len(input.Changes) > 0 {
			changed[input.Step] = true
		}
	}
	if len(changed) == 0 {
		return nil
	}
	steps := make(map[string]*ir.Step, len(dag.Steps))
	for i := range dag.Steps {
		steps[dag.Steps[i].Name] = &dag.Steps[i]
	}
	tainted := make(map[string]bool, len(dag.Steps))
	visited := make(map[string]bool, len(dag.Steps))
	var visit func(name string) bool
	visit = func(name string) bool {
		if visited[name] {
			return tainted[name]
		}
		visited[name] = true
		tainted[name] = changed[name]
		if step, ok := steps[name]; ok {
			for _, dep := range step.AllDepends() {
				if visit(dep) {
					tainted[name] = true
				}
			}
		}
		return tainted[name]
	}
	for name := range steps {
		visit(name)
	}
	return tainted
}

// joinWords joins words as a list in an English sentence.
func joinWords(words []string) string {
	switch len(words) {
	case 0:
		return ""
	case 1:
		return words[0]
	case 2:
		return words[0] + " and " + words[1]
	}
	return strings.Join(words[:len(words)-1], ", ") + ", and " + words[len(words)-1]
}

// handlerOnKey is the key of the handlers in a definition and a base config.
const handlerOnKey = "handler_on"

// fingerprint holds, as digests, what decides what each step of a definition
// does, so that two definitions compare part by part.
type fingerprint struct {
	// settings is the DAG's definition other than its steps.
	settings   string
	baseConfig string
	params     string
	profile    string
	steps      map[string]stepDigests
}

// stepDigests are the digests of one step: its own definition, the steps it
// depends on, and both together.
type stepDigests struct {
	own   string
	deps  string
	chain string
}

// newFingerprint returns the fingerprint of dag run with params and profile.
func newFingerprint(dag *ir.DAG, params, profile string) (*fingerprint, error) {
	settings, err := settingsDigest(dag.YamlData)
	if err != nil {
		return nil, err
	}
	base, err := baseConfigDigest(dag.BaseConfigData)
	if err != nil {
		return nil, err
	}
	steps, err := stepDigestsOf(dag)
	if err != nil {
		return nil, err
	}
	return &fingerprint{settings: settings, baseConfig: base, params: params, profile: profile, steps: steps}, nil
}

// changes lists what changed for step name since earlier, in the order of the
// StepChange constants. It is empty when the step can be reused.
func (f *fingerprint) changes(earlier *fingerprint, name string) []ir.StepChange {
	var changes []ir.StepChange
	now := f.steps[name]
	then, existed := earlier.steps[name]
	if !existed || now.own != then.own {
		changes = append(changes, ir.StepChangeStep)
	}
	if existed && now.deps != then.deps {
		changes = append(changes, ir.StepChangeDependency)
	}
	if f.params != earlier.params {
		changes = append(changes, ir.StepChangeParams)
	}
	if f.profile != earlier.profile {
		changes = append(changes, ir.StepChangeProfile)
	}
	if f.baseConfig != earlier.baseConfig {
		changes = append(changes, ir.StepChangeBaseConfig)
	}
	if f.settings != earlier.settings {
		changes = append(changes, ir.StepChangeSettings)
	}
	return changes
}

// metadataKeys are the top-level keys of a definition that describe, schedule,
// notify, retain, or limit runs. They cannot change what a step that succeeded
// gave, so a change of them does not count. The web UI leaves out the same keys
// (runSpecDiff.ts).
var metadataKeys = []string{
	"group", "description", "labels", "tags",
	"schedule", "skip_if_successful", "catchup_window", "overlap_policy", "webhook", "watch",
	"smtp", "mail_accounts", "mail_on", "error_mail", "info_mail", "wait_mail",
	"hist_retention_days", "hist_retention_runs", "slot_retention_days", "log_dir", "otel",
	"queue", "max_active_runs", "max_active_steps", "timeout_sec", "delay_sec",
	"restart_wait_sec", "restart_policy", "retry_policy", "max_clean_up_time_sec",
}

// settingsDigest is a digest of the DAG's definition other than its steps, as
// written, with the local sub-DAGs that follow it in the file. The parameters
// count as the run resolved them instead, of the handlers only init counts
// (it runs before the steps, the others after them), and metadataKeys do not
// count.
func settingsDigest(yamlData []byte) (string, error) {
	if len(yamlData) == 0 {
		return "", errors.New("the DAG keeps no definition to compare")
	}
	documents, err := spec.Documents(yamlData)
	if err != nil {
		return "", fmt.Errorf("read the DAG's definition: %w", err)
	}
	if len(documents) > 0 {
		head := documents[0]
		delete(head, "steps")
		delete(head, "params")
		keepBehavior(head)
	}
	return digestOf(documents)
}

// baseConfigDigest is a digest of the base config as values. SMTP only sends
// mail, and the base config counts as a definition does.
func baseConfigDigest(data []byte) (string, error) {
	values, err := spec.BaseConfigValues(data)
	if err != nil {
		return "", fmt.Errorf("read the base config: %w", err)
	}
	keepBehavior(values)
	if len(values) == 0 {
		return "", nil
	}
	return digestOf(values)
}

// keepBehavior leaves in the values of a definition what can change what a
// step does: no metadataKeys, and only the init handler.
func keepBehavior(values map[string]any) {
	for _, key := range metadataKeys {
		delete(values, key)
	}
	keepInitHandler(values)
}

// keepInitHandler leaves only the init handler in the values of a definition:
// the other handlers run after the steps and cannot change what a step does.
func keepInitHandler(values map[string]any) {
	handlers, ok := values[handlerOnKey].(map[string]any)
	if !ok {
		return
	}
	if init, ok := handlers["init"]; ok {
		values[handlerOnKey] = map[string]any{"init": init}
		return
	}
	delete(values, handlerOnKey)
}

// stepDigestsOf returns the digests of each step of dag by step name.
func stepDigestsOf(dag *ir.DAG) (map[string]stepDigests, error) {
	steps := make(map[string]*ir.Step, len(dag.Steps))
	for i := range dag.Steps {
		steps[dag.Steps[i].Name] = &dag.Steps[i]
	}
	digests := make(map[string]stepDigests, len(dag.Steps))
	visiting := make(map[string]bool, len(dag.Steps))
	var visit func(name string) (stepDigests, error)
	visit = func(name string) (stepDigests, error) {
		if d, ok := digests[name]; ok {
			return d, nil
		}
		step, ok := steps[name]
		if !ok {
			return stepDigests{}, fmt.Errorf("step %q depends on a step the DAG does not have", name)
		}
		if visiting[name] {
			return stepDigests{}, fmt.Errorf("step %q depends on itself", name)
		}
		visiting[name] = true
		definition, err := stepDefinition(step)
		if err != nil {
			return stepDigests{}, err
		}
		deps := sha256.New()
		for _, dep := range slices.Sorted(slices.Values(step.AllDepends())) {
			d, err := visit(dep)
			if err != nil {
				return stepDigests{}, err
			}
			_, _ = fmt.Fprintf(deps, "%s=%s\x00", dep, d.chain)
		}
		d := stepDigests{own: hashOf(definition), deps: hex.EncodeToString(deps.Sum(nil))}
		d.chain = hashOf([]byte(d.own + "\x00" + d.deps))
		digests[name] = d
		return d, nil
	}
	for name := range steps {
		if _, err := visit(name); err != nil {
			return nil, err
		}
	}
	return digests, nil
}

// digestOf is a digest of value as JSON, which sorts the keys of its maps.
func digestOf(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("read the DAG's definition: %w", err)
	}
	return hashOf(data), nil
}

// hashOf is the SHA-256 of data in hex.
func hashOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
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

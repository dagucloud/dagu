// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"

	"github.com/dagucloud/dagu/v2/internal/build"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// MaxPushBackInputsSize is the largest JSON encoding of the inputs one
// push-back may record. Rewound steps receive the inputs as environment
// variables, and a single variable is limited to 32,767 characters on Windows
// and 128 KiB on Linux.
const MaxPushBackInputsSize = 16 << 10

// ValidatePushBackInputsSize reports an error when inputs exceed
// MaxPushBackInputsSize.
func ValidatePushBackInputsSize(inputs map[string]string) error {
	if len(inputs) == 0 {
		return nil
	}
	data, err := json.Marshal(inputs)
	if err != nil {
		return fmt.Errorf("encode push-back inputs: %w", err)
	}
	if len(data) > MaxPushBackInputsSize {
		return fmt.Errorf("push-back inputs exceed the maximum size of %d bytes", MaxPushBackInputsSize)
	}
	return nil
}

// FilterPushBackInputs returns only declared push-back inputs. If no allowlist
// is provided, the stored inputs are preserved as-is.
func FilterPushBackInputs(allowed []string, inputs map[string]string) map[string]string {
	if len(inputs) == 0 {
		return nil
	}
	if len(allowed) == 0 {
		return maps.Clone(inputs)
	}

	allowedSet := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		allowedSet[key] = struct{}{}
	}

	filtered := make(map[string]string)
	for key, value := range inputs {
		if _, ok := allowedSet[key]; ok {
			filtered[key] = value
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

// NormalizePushBackHistory returns the history a step with the given input
// allowlist observes, seeded from legacy state when only the latest
// iteration/input pair is available. Human-task feedback is never filtered.
func NormalizePushBackHistory(
	allowed []string,
	iteration int,
	latestInputs map[string]string,
	history []ir.PushBackEntry,
) []ir.PushBackEntry {
	return normalizePushBackHistory(allowed, iteration, latestInputs, history, func(entry ir.PushBackEntry) bool {
		return entry.HumanTask
	})
}

// normalizePushBackHistory seeds legacy history and filters every entry that
// keep does not exempt.
func normalizePushBackHistory(
	allowed []string,
	iteration int,
	latestInputs map[string]string,
	history []ir.PushBackEntry,
	keep func(ir.PushBackEntry) bool,
) []ir.PushBackEntry {
	normalized := ClonePushBackHistory(history)
	if len(normalized) == 0 && iteration > 0 {
		normalized = append(normalized, ir.PushBackEntry{
			Iteration: iteration,
			Inputs:    FilterPushBackInputs(allowed, latestInputs),
		})
	}
	for i := range normalized {
		if keep(normalized[i]) {
			continue
		}
		normalized[i].Inputs = FilterPushBackInputs(allowed, normalized[i].Inputs)
	}
	return normalized
}

// VisiblePushBackInputs returns the latest push-back inputs a step with the
// given input allowlist receives. Feedback from a human task is never
// filtered because it holds only declared feedback properties.
func VisiblePushBackInputs(allowed []string, latest map[string]string, history []ir.PushBackEntry) map[string]string {
	if n := len(history); n > 0 && history[n-1].HumanTask {
		return maps.Clone(latest)
	}
	return FilterPushBackInputs(allowed, latest)
}

// ClonePushBackHistory returns a deep copy of push-back history entries.
func ClonePushBackHistory(src []ir.PushBackEntry) []ir.PushBackEntry {
	if len(src) == 0 {
		return nil
	}

	dst := make([]ir.PushBackEntry, len(src))
	for i, entry := range src {
		dst[i] = ir.PushBackEntry{
			Iteration: entry.Iteration,
			By:        entry.By,
			ByID:      entry.ByID,
			At:        entry.At,
			Inputs:    maps.Clone(entry.Inputs),
			Step:      entry.Step,
			HumanTask: entry.HumanTask,
		}
	}
	return dst
}

// PushBack describes one push-back of a waiting manual step.
type PushBack struct {
	// TargetName is the step that runs again first.
	TargetName string
	// AllowedInputs restricts which inputs are recorded. Empty keeps them all.
	AllowedInputs []string
	// Inputs holds the reviewer feedback.
	Inputs map[string]string
	// By, ByID, and At identify who pushed the step back and when.
	By   string
	ByID string
	At   string
}

// ApplyPushBack resets the target step and every step that depends on it,
// directly or transitively, for another execution. Each reset step records the
// push-back iteration, the feedback, the source step's push-back history, and
// its own previous stdout. The new iteration exceeds the iteration of the
// source and of every reset step, and is returned.
func ApplyPushBack(status *ir.DAGRunStatus, source *ir.Node, pb PushBack) (int, error) {
	target := nodeByStepName(status.Nodes, pb.TargetName)
	if target == nil {
		return 0, fmt.Errorf("push-back target step %s does not exist", pb.TargetName)
	}

	rewound := append([]*ir.Node{target}, dependentNodes(status.Nodes, pb.TargetName)...)
	iteration := source.ApprovalIteration
	for _, node := range rewound {
		iteration = max(iteration, node.ApprovalIteration)
	}
	iteration++
	inputs := FilterPushBackInputs(pb.AllowedInputs, pb.Inputs)
	history := append(
		// Entries that name their step were scoped when that step recorded
		// them; only legacy entries take the new source's allowlist.
		normalizePushBackHistory(pb.AllowedInputs, source.ApprovalIteration, source.PushBackInputs, source.PushBackHistory,
			func(entry ir.PushBackEntry) bool { return entry.Step != "" }),
		ir.PushBackEntry{
			Iteration: iteration,
			By:        pb.By,
			ByID:      pb.ByID,
			At:        pb.At,
			Inputs:    maps.Clone(inputs),
			Step:      source.Step.Name,
			HumanTask: source.Step.HumanTask != nil,
		},
	)

	for _, node := range rewound {
		previousStdout := node.Stdout
		*node = *ir.NewNodeFromStep(node.Step)
		node.ApprovalIteration = iteration
		node.PushBackInputs = maps.Clone(inputs)
		node.PushBackHistory = ClonePushBackHistory(history)
		node.PushBackPreviousStdout = previousStdout
	}
	return iteration, nil
}

// RevertPushBack restores in latest every step that differs between original,
// the status before a push-back, and applied, the status the push-back
// produced. It fails without changing latest when any of those steps changed
// after the push-back.
func RevertPushBack(latest, original, applied *ir.DAGRunStatus) error {
	if latest == nil || original == nil || applied == nil {
		return errors.New("push-back rollback status is nil")
	}
	type change struct {
		latest   *ir.Node
		original *ir.Node
	}
	var changes []change
	for _, originalNode := range original.Nodes {
		if originalNode == nil {
			continue
		}
		appliedNode := nodeByStepName(applied.Nodes, originalNode.Step.Name)
		if appliedNode == nil {
			return fmt.Errorf("pushed-back step %s is missing", originalNode.Step.Name)
		}
		if reflect.DeepEqual(originalNode, appliedNode) {
			continue
		}
		latestNode := nodeByStepName(latest.Nodes, originalNode.Step.Name)
		if latestNode == nil || !reflect.DeepEqual(latestNode, appliedNode) {
			return fmt.Errorf("step %s changed after push-back", originalNode.Step.Name)
		}
		changes = append(changes, change{latest: latestNode, original: originalNode})
	}
	for _, c := range changes {
		*c.latest = *c.original
	}
	return nil
}

func nodeByStepName(nodes []*ir.Node, name string) *ir.Node {
	for _, node := range nodes {
		if node != nil && node.Step.Name == name {
			return node
		}
	}
	return nil
}

// dependentNodes returns the steps that depend on stepName directly or
// transitively, excluding stepName itself.
func dependentNodes(nodes []*ir.Node, stepName string) []*ir.Node {
	dependencies := stepDependencies(nodes)
	dependents := map[string]bool{stepName: true}
	for changed := true; changed; {
		changed = false
		for _, node := range nodes {
			if node == nil || dependents[node.Step.Name] {
				continue
			}
			for _, dep := range dependencies[node.Step.Name] {
				if dependents[dep] {
					dependents[node.Step.Name] = true
					changed = true
					break
				}
			}
		}
	}

	var result []*ir.Node
	for _, node := range nodes {
		if node != nil && node.Step.Name != stepName && dependents[node.Step.Name] {
			result = append(result, node)
		}
	}
	return result
}

// stepDependencies returns each step's declared dependencies plus the build
// producers whose output paths it consumes, since build steps link through
// declared paths without declaring depends. Stored steps carry the paths the
// run resolved.
func stepDependencies(nodes []*ir.Node) map[string][]string {
	dependencies := make(map[string][]string, len(nodes))
	hasInputs := false
	for _, node := range nodes {
		if node == nil {
			continue
		}
		dependencies[node.Step.Name] = node.Step.Depends
		hasInputs = hasInputs || len(node.Step.Inputs) > 0
	}
	if !hasInputs {
		return dependencies
	}

	pathKeys := build.NewPathKeyResolver()
	producers := make(map[string]string)
	for _, node := range nodes {
		if node == nil {
			continue
		}
		for _, output := range node.Step.Outputs {
			if output.Path != "" {
				producers[pathKeys.ComparisonKey(output.Path)] = node.Step.Name
			}
		}
	}
	for _, node := range nodes {
		if node == nil {
			continue
		}
		for _, input := range node.Step.Inputs {
			producer, ok := producers[pathKeys.ComparisonKey(input.Path)]
			if ok && producer != node.Step.Name {
				dependencies[node.Step.Name] = append(slices.Clone(dependencies[node.Step.Name]), producer)
			}
		}
	}
	return dependencies
}

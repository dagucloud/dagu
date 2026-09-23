// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"errors"
	"fmt"
	"maps"
	"reflect"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

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

// NormalizePushBackHistory ensures stored history is filtered and seeded from
// legacy state when only the latest iteration/input pair is available.
func NormalizePushBackHistory(
	allowed []string,
	iteration int,
	latestInputs map[string]string,
	history []ir.PushBackEntry,
) []ir.PushBackEntry {
	normalized := ClonePushBackHistory(history)
	if len(normalized) == 0 && iteration > 0 {
		normalized = append(normalized, ir.PushBackEntry{
			Iteration: iteration,
			Inputs:    FilterPushBackInputs(allowed, latestInputs),
		})
	}
	for i := range normalized {
		normalized[i].Inputs = FilterPushBackInputs(allowed, normalized[i].Inputs)
	}
	return normalized
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
// its own previous stdout. It returns the new iteration.
func ApplyPushBack(status *ir.DAGRunStatus, source *ir.Node, pb PushBack) (int, error) {
	target := nodeByStepName(status.Nodes, pb.TargetName)
	if target == nil {
		return 0, fmt.Errorf("push-back target step %s does not exist", pb.TargetName)
	}

	iteration := source.ApprovalIteration + 1
	inputs := FilterPushBackInputs(pb.AllowedInputs, pb.Inputs)
	history := append(
		NormalizePushBackHistory(pb.AllowedInputs, source.ApprovalIteration, source.PushBackInputs, source.PushBackHistory),
		ir.PushBackEntry{
			Iteration: iteration,
			By:        pb.By,
			ByID:      pb.ByID,
			At:        pb.At,
			Inputs:    maps.Clone(inputs),
		},
	)

	for _, node := range append([]*ir.Node{target}, dependentNodes(status.Nodes, pb.TargetName)...) {
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
	dependents := map[string]bool{stepName: true}
	for changed := true; changed; {
		changed = false
		for _, node := range nodes {
			if node == nil || dependents[node.Step.Name] {
				continue
			}
			for _, dep := range node.Step.Depends {
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

// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"fmt"
	"strings"

	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// inferStepOutputDependencies adds an inferred edge from every producer that a
// strict step-output reference names to the top-level step owning the field.
// A reference inside a foreach body attaches the edge to the owning foreach
// step. Producers the owner already reaches through explicit or earlier
// inferred edges add nothing. An edge that would close a cycle is an error.
//
// Dependencies must already be resolved to runtime step names.
func inferStepOutputDependencies(dag *ir.DAG, errs *ir.ErrorList) {
	if dag == nil || dag.Type == ir.TypeAgent {
		return
	}
	idToIndex := make(map[string]int, len(dag.Steps))
	nameToIndex := make(map[string]int, len(dag.Steps))
	for i := range dag.Steps {
		nameToIndex[dag.Steps[i].Name] = i
		if dag.Steps[i].ID != "" {
			idToIndex[dag.Steps[i].ID] = i
		}
	}
	// Lookups read dag.Steps by index so that edges appended during the loop
	// are visible to later reachability checks.
	stepByName := func(name string) (ir.Step, bool) {
		i, ok := nameToIndex[name]
		if !ok {
			return ir.Step{}, false
		}
		return dag.Steps[i], true
	}

	for _, field := range ReferenceFields(dag) {
		if !inferredDependencyField(field) {
			continue
		}
		owner := &dag.Steps[field.topLevelStepIndex]
		for _, ref := range cmnvalue.StepOutputReferences(field.Value) {
			producerIndex, ok := idToIndex[ref.StepName]
			if !ok || producerIndex == field.topLevelStepIndex {
				continue
			}
			producer := dag.Steps[producerIndex].Name
			if reachesStep(stepByName, owner.Name, producer) {
				continue
			}
			if reachesStep(stepByName, producer, owner.Name) {
				*errs = append(*errs, ir.NewValidationError(field.Path, ref.Expression,
					fmt.Errorf("inferred dependency %s -> %s creates a cycle", producer, owner.Name)))
				continue
			}
			owner.InferredDepends = append(owner.InferredDepends, ir.InferredDependency{
				Step:  producer,
				Field: field.noticeFieldPath(),
			})
		}
	}
}

// inferredDependencyField reports whether a step-output reference in the field
// orders the owning top-level step after the producer. Fields without
// step-output lookup scope, item-scoped foreach fields, and template executor
// scripts rendered as written create no edge.
func inferredDependencyField(field ReferenceField) bool {
	if field.topLevelStepIndex < 0 || !strings.Contains(field.Value, "$") {
		return false
	}
	if field.Field.IsTemplateScript() {
		return false
	}
	return !isForeachItemFieldPath(field.noticeFieldPath())
}

// reachesStep reports whether from depends on target directly or transitively
// through explicit and inferred edges.
func reachesStep(stepByName func(string) (ir.Step, bool), from, target string) bool {
	start, ok := stepByName(from)
	if !ok {
		return false
	}
	queue := append([]string(nil), start.AllDepends()...)
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
		if step, ok := stepByName(current); ok {
			queue = append(queue, step.AllDepends()...)
		}
	}
	return false
}

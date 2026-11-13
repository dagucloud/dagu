// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/reffield"
)

// stepReads is what the value fields of a step read from other steps.
type stepReads struct {
	// text holds the references that insert text a step produced.
	text []cmnvalue.StepTextReference
	// values holds the field values, where output variables are read by name.
	values []string
}

// stepReads returns what the fields of the node's step read.
func (n *Node) stepReads() stepReads {
	n.readsOnce.Do(func() {
		for _, field := range reffield.ReferenceFields(&ir.DAG{Steps: []ir.Step{n.Step()}}) {
			n.reads.text = append(n.reads.text, field.Field.StepTextReferences(field.Value)...)
			n.reads.values = append(n.reads.values, field.Value)
		}
	})
	return n.reads
}

// checkMaskedReads fails a step that reads an output a predecessor restored
// masked. It runs once the preconditions hold, since a skipped step reads
// nothing. A run that restored no masked output skips the predecessor walk.
func (r *Runner) checkMaskedReads(plan *Plan, node *Node) error {
	if !slices.ContainsFunc(plan.Nodes(), func(n *Node) bool { return n.State().MaskedOutputs != nil }) {
		return nil
	}
	return checkMaskedOutputs(planPredecessorNodes(plan, node), node, false)
}

// checkMaskedOutputs fails a step that reads an output of one of sources that
// was restored from a status stored with that output masked. The original text
// is gone, so the step would run on starred text. A handler has no step-output
// scope, so only its reads of output variables count.
func checkMaskedOutputs(sources []*Node, node *Node, handler bool) error {
	for _, source := range sources {
		masked := source.State().MaskedOutputs
		if masked == nil {
			continue
		}
		name, ok := maskedRead(node.stepReads(), source, masked, handler)
		if !ok {
			continue
		}
		return fmt.Errorf(
			"step %q reads output %q of step %q, which was stored masked because it holds a secret; step %q has to run again to produce it",
			node.Name(), name, source.Name(), source.Name(),
		)
	}
	return nil
}

// maskedRead returns the first masked output of source that reads names.
func maskedRead(reads stepReads, source *Node, masked *ir.MaskedOutputs, handler bool) (string, bool) {
	// Step references resolve by step id only.
	if id := source.Step().ID; id != "" && !handler {
		for _, ref := range reads.text {
			if ref.StepName != id {
				continue
			}
			if ref.Field == "" {
				// The whole output holds every masked part of it.
				if masked.All || len(masked.Names) > 0 {
					return ref.Expression, true
				}
				continue
			}
			if masked.Has(ref.Field) {
				return ref.Field, true
			}
		}
	}
	variables := source.OutputVariablesMap()
	for _, name := range slices.Sorted(maps.Keys(variables)) {
		if masked.Has(name) && slices.ContainsFunc(reads.values, func(value string) bool {
			return readsVariable(value, name)
		}) {
			return name, true
		}
	}
	return "", false
}

// readsVariable reports whether value reads the variable as $NAME or ${NAME}.
// A dollar after an odd number of backslashes is escaped, so it is text.
func readsVariable(value, name string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] != '$' || escapedDollar(value, i) {
			continue
		}
		rest := value[i+1:]
		braced := strings.HasPrefix(rest, "{")
		if braced {
			rest = strings.TrimLeft(rest[1:], " ")
		}
		after, ok := strings.CutPrefix(rest, name)
		if !ok || (after != "" && isNameByte(after[0])) {
			continue
		}
		return true
	}
	return false
}

func escapedDollar(value string, at int) bool {
	backslashes := 0
	for i := at - 1; i >= 0 && value[i] == '\\'; i-- {
		backslashes++
	}
	return backslashes%2 == 1
}

func isNameByte(b byte) bool {
	return b == '_' || ('0' <= b && b <= '9') || ('a' <= b && b <= 'z') || ('A' <= b && b <= 'Z')
}

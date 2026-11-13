// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/reffield"
)

// stepReads is what the value fields of a step read from other steps.
type stepReads struct {
	// text holds the references that insert text a step produced.
	text []cmnvalue.StepTextReference
	// envFields holds the fields of the step's own env. Its entries resolve in
	// order, with the predecessors' output variables in scope.
	envFields []envField
	// otherFields holds the fields outside the step's own env.
	otherFields []reffield.ReferenceField
	// envNames holds the names the step's own env defines, in entry order.
	envNames []string
}

// envField is a field of the step's own env.
type envField struct {
	reffield.ReferenceField
	// entry is the index of the env entry that holds the field.
	entry int
}

// stepReads returns what the fields of the node's step read.
func (n *Node) stepReads() stepReads {
	n.readsOnce.Do(func() {
		for _, field := range reffield.ReferenceFields(&ir.DAG{Steps: []ir.Step{n.Step()}}) {
			n.reads.text = append(n.reads.text, field.Field.StepTextReferences(field.Value)...)
			if index, ok := strings.CutPrefix(field.Path, field.OwnerStepPath+".env["); ok {
				entry, _ := strconv.Atoi(strings.TrimSuffix(index, "]"))
				n.reads.envFields = append(n.reads.envFields, envField{ReferenceField: field, entry: entry})
			} else {
				n.reads.otherFields = append(n.reads.otherFields, field)
			}
		}
		for _, entry := range n.Step().Env {
			name, _, _ := strings.Cut(entry, "=")
			n.reads.envNames = append(n.reads.envNames, name)
		}
	})
	return n.reads
}

// checkMaskedReads fails a step that reads an output a predecessor restored
// masked. It runs once the preconditions hold, since a skipped step reads
// nothing. A run that restored no masked output skips the predecessor walk.
func (r *Runner) checkMaskedReads(plan *Plan, node *Node) error {
	if !plan.hasMaskedOutputs() {
		return nil
	}
	return checkMaskedOutputs(planPredecessorNodes(plan, node), node, false)
}

// checkMaskedHandlerReads is checkMaskedReads for a handler, which sees the
// outputs of every step.
func (r *Runner) checkMaskedHandlerReads(plan *Plan, node *Node) error {
	if !plan.hasMaskedOutputs() {
		return nil
	}
	return checkMaskedOutputs(plan.Nodes(), node, true)
}

// hasMaskedOutputs reports whether a node of the plan was restored with
// masked outputs. Only a restored status records them, and a step that runs
// again drops its record, so a plan without one at the first step start
// never gains one.
func (p *Plan) hasMaskedOutputs() bool {
	p.maskedOnce.Do(func() {
		p.maskedOutputs = slices.ContainsFunc(p.Nodes(), func(n *Node) bool { return n.MaskedOutputs() != nil })
	})
	return p.maskedOutputs
}

// checkMaskedOutputs fails a step that reads an output of one of sources that
// was restored from a status stored with that output masked. The original text
// is gone, so the step would run on starred text. A handler has no
// ${steps...} lookup scope, so those references are no reads there.
func checkMaskedOutputs(sources []*Node, node *Node, handler bool) error {
	for _, source := range sources {
		masked := source.MaskedOutputs()
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
	state := source.State()
	step := source.Step()
	// Step references resolve by step id only.
	if step.ID != "" {
		for _, ref := range reads.text {
			if ref.StepName != step.ID || !readsMaskedText(ref, step, state, masked, handler) {
				continue
			}
			if ref.Field != "" {
				return ref.Field, true
			}
			return ref.Expression, true
		}
	}
	variables := source.OutputVariablesMap()
	for _, name := range masked.Variables {
		if _, ok := variables[name]; !ok {
			continue
		}
		if reads.readsOutputVariable(name, handler) {
			return name, true
		}
	}
	return "", false
}

// readsOutputVariable reports whether a field of the step reads a
// predecessor's output variable. A handler resolves its env before output
// variables enter scope, so only its other fields read them, and there they
// win. A step resolves its env entries in order after output variables, so an
// entry that defines the name shadows it in the later entries and in the
// other fields.
func (r stepReads) readsOutputVariable(name string, handler bool) bool {
	reads := func(field reffield.ReferenceField) bool {
		return readsVariable(field.Value, name, field.Field.EscapesDollar())
	}
	if handler {
		return slices.ContainsFunc(r.otherFields, reads)
	}
	if !slices.Contains(r.envNames, name) && slices.ContainsFunc(r.otherFields, reads) {
		return true
	}
	return slices.ContainsFunc(r.envFields, func(field envField) bool {
		return !slices.Contains(r.envNames[:field.entry], name) && reads(field.ReferenceField)
	})
}

// readsMaskedText reports whether ref reads a masked output of the step. It
// picks the output the reference resolves from the way Data.StepInfo does.
func readsMaskedText(ref cmnvalue.StepTextReference, step ir.Step, state NodeState, masked *ir.MaskedOutputs, handler bool) bool {
	path := referencePath(ref)
	switch {
	case strings.HasPrefix(path, "steps."):
		// A handler has no ${steps...} lookup scope, so the reference reads
		// nothing there.
		return !handler && masked.HasPublished(ref.Field)
	case strings.HasPrefix(path, ref.StepName+".outputs"):
		// Published outputs take precedence over the captured document. The
		// captured flag also covers the captured text, so a step with only
		// masked text fails a read here that resolves to nothing anyway.
		if state.StepOutputsValue == nil {
			return masked.HasCaptured()
		}
		if ref.Field == "" {
			return masked.AllPublished || len(masked.Published) > 0
		}
		return masked.HasPublished(ref.Field)
	default:
		// ${<id>.output} falls back to the output variable when no captured
		// text is stored.
		if state.OutputValue != nil {
			return masked.HasCaptured()
		}
		return masked.HasVariable(step.Output)
	}
}

// referencePath returns the reference without its dollar and braces.
func referencePath(ref cmnvalue.StepTextReference) string {
	path := strings.TrimPrefix(ref.Expression, "$")
	path = strings.TrimPrefix(path, "{")
	return strings.TrimSpace(strings.TrimSuffix(path, "}"))
}

// readsVariable reports whether value reads the variable as $NAME or ${NAME}.
// When escapes is set, a dollar after an odd number of backslashes is escaped,
// so it is text.
func readsVariable(value, name string, escapes bool) bool {
	for i := 0; i < len(value); i++ {
		if value[i] != '$' || (escapes && escapedDollar(value, i)) {
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

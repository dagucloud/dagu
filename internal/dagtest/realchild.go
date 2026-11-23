// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagtest

import (
	"fmt"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// CheckRealChildren returns what makes the child DAGs that the real steps of
// c start in the case invalid (Spec 101): a child that resolve cannot find
// before the run, and every source of the fail-closed rule in a child it
// finds. resolve returns the child DAG of a name as its run would load it.
// A child takes no mocks and no real, so no field of the case handles its
// sources. dag is the tested DAG of c.
func CheckRealChildren(dag *ir.DAG, c Case, resolve func(name string) (*ir.DAG, error)) []Finding {
	var findings []Finding
	for _, t := range indexSteps(dag).all {
		if !c.runsReal(t) || !startsChildRun(*t.step) {
			continue
		}
		label := t.label
		if t.handler == "" {
			label = "step " + label
		}
		fix := "a child of a real step takes no mock; mock " + t.label + " instead"
		name := t.step.SubDAG.Name
		if strings.Contains(name, "$") {
			findings = append(findings, Finding{Source: label, Message: fmt.Sprintf("child DAG %s is known only during the run; mock it", name)})
			continue
		}
		child, err := resolve(name)
		if err != nil {
			findings = append(findings, Finding{Source: label, Message: fmt.Sprintf("child DAG %s cannot be resolved: %v; mock it", name, err)})
			continue
		}
		check := checker{fix: fix}
		check.checkDAG(child, Handling{}, true)
		for _, f := range check.findings {
			findings = append(findings, Finding{Source: label + ", child " + name + ", " + f.Source, Message: f.Message})
		}
	}
	return findings
}

// runsReal reports whether the case runs the step or handler for real.
func (c Case) runsReal(t *stepTarget) bool {
	if t.handler != "" {
		return c.Handlers[t.handler].Real
	}
	return c.Steps[t.step.Name].Real
}

// startsChildRun reports whether the step runs a child DAG in the run that
// starts it, as dag.run and parallel do. dag.enqueue only queues its child.
func startsChildRun(step ir.Step) bool {
	return step.SubDAG != nil && step.ExecutorConfig.Type != ir.ExecutorTypeDAGEnqueue
}

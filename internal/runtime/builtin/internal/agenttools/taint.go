// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agenttools

import (
	"fmt"
	"regexp"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// substitutedRef matches the references that Dagu writes into command text
// itself: ${params.NAME} and ${env.NAME}. The shell reads $NAME and ${NAME},
// so their value stays data.
var substitutedRef = regexp.MustCompile(`\$\{\s*(params|env)\.([A-Za-z_][A-Za-z0-9_]*)\s*\}`)

// checkSubstitutedInputs fails when a command text of the tool DAG refers to an
// input of the model with a reference that Dagu substitutes.
func checkSubstitutedInputs(dag *ir.DAG, inputs []toolParam) error {
	if dag == nil || len(inputs) == 0 {
		return nil
	}
	model := make(map[string]struct{}, len(inputs))
	for _, input := range inputs {
		model[input.Name] = struct{}{}
	}
	steps := make([]*ir.Step, 0, len(dag.Steps)+6)
	for i := range dag.Steps {
		steps = append(steps, &dag.Steps[i])
	}
	h := dag.HandlerOn
	steps = append(steps, h.Init, h.Success, h.Failure, h.Abort, h.Exit, h.Wait)
	for _, step := range steps {
		if step == nil {
			continue
		}
		for _, text := range commandTexts(step) {
			for _, match := range substitutedRef.FindAllStringSubmatch(text, -1) {
				if _, ok := model[match[2]]; ok {
					return fmt.Errorf("tool DAG %q, step %q: the command refers to %s, an input that the model sets, and Dagu puts its value in the command; write \"$%s\" so that the shell reads it as data",
						dag.Name, step.Name, match[0], match[2])
				}
			}
		}
	}
	return nil
}

// commandTexts returns the texts of a step that become a command or a script.
func commandTexts(step *ir.Step) []string {
	texts := []string{step.Command, step.CmdWithArgs, step.CmdArgsSys, step.ShellCmdArgs, step.Script}
	texts = append(texts, step.Args...)
	for _, entry := range step.Commands {
		texts = append(texts, entry.Command, entry.CmdWithArgs)
		texts = append(texts, entry.Args...)
	}
	return texts
}

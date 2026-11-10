// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// commandStep returns a local command step that runs each of commands.
func commandStep(commands ...string) ir.Step {
	step := ir.Step{Name: "s"}
	for _, command := range commands {
		step.Commands = append(step.Commands, ir.CommandEntry{Command: command})
	}
	return step
}

func TestFindMissingCommands(t *testing.T) {
	t.Parallel()

	absolute := filepath.Join(t.TempDir(), "tool")
	nothingExists := func(string) bool { return false }

	tests := []struct {
		name    string
		shell   string
		step    ir.Step
		dag     func(*ir.DAG)
		exists  func(string) bool
		missing []string
	}{
		{name: "AbsolutePath", shell: "/bin/sh", step: commandStep(absolute), missing: []string{absolute}},
		{name: "AbsolutePathInOtherShell", shell: "pwsh", step: commandStep(absolute), missing: []string{absolute}},
		{name: "BareName", shell: "/bin/bash -e", step: commandStep("my-cmd"), missing: []string{"my-cmd"}},
		{name: "BareNameWithoutShell", shell: "direct", step: commandStep("cd"), missing: []string{"cd"}},
		{
			name:   "ExistingCommand",
			shell:  "/bin/sh",
			step:   commandStep("my-cmd"),
			exists: func(command string) bool { return command == "my-cmd" },
		},
		{name: "ShellBuiltin", shell: "/bin/sh", step: commandStep("cd", "export", "if")},
		// The built-ins of other shells are unknown, so a bare name may be one.
		{name: "BareNameInOtherShell", shell: "pwsh", step: commandStep("Get-ChildItem")},
		{name: "Variable", shell: "/bin/sh", step: commandStep("${BIN}/tool", "$TOOL")},
		{name: "Substitution", shell: "/bin/sh", step: commandStep("$(which tool)", "`which tool`")},
		{name: "Assignment", shell: "/bin/sh", step: commandStep("FOO=1")},
		{name: "HomeDirectory", shell: "/bin/sh", step: commandStep("~/bin/tool")},
		{name: "RelativePath", shell: "/bin/sh", step: commandStep("./tool", "bin/tool")},
		{
			name:  "Script",
			shell: "/bin/sh",
			step:  ir.Step{Name: "s", Script: "my-cmd", Commands: []ir.CommandEntry{{Command: "my-interpreter"}}},
		},
		{
			name:  "ContainerStep",
			shell: "/bin/sh",
			step:  ir.Step{Name: "s", Container: &ir.Container{}, Commands: []ir.CommandEntry{{Command: absolute}}},
		},
		{
			name:  "OtherExecutor",
			shell: "/bin/sh",
			step: ir.Step{
				Name:           "s",
				ExecutorConfig: ir.ExecutorConfig{Type: "ssh"},
				Commands:       []ir.CommandEntry{{Command: absolute}},
			},
		},
		{
			name:  "StepSetsPath",
			shell: "/bin/sh",
			step:  ir.Step{Name: "s", Env: []string{"PATH=/opt/bin"}, Commands: []ir.CommandEntry{{Command: "my-cmd"}}},
		},
		{
			name:  "DAGSetsPath",
			shell: "/bin/sh",
			step:  commandStep("my-cmd"),
			dag:   func(dag *ir.DAG) { dag.Env = []string{"PATH=/opt/bin"} },
		},
		{
			name:  "DAGInstallsTools",
			shell: "/bin/sh",
			step:  commandStep("my-cmd"),
			dag:   func(dag *ir.DAG) { dag.Tools = &ir.ToolConfig{} },
		},
		{
			name:    "AbsolutePathWhenDAGSetsPath",
			shell:   "/bin/sh",
			step:    commandStep(absolute),
			dag:     func(dag *ir.DAG) { dag.Env = []string{"PATH=/opt/bin"} },
			missing: []string{absolute},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dag := &ir.DAG{Steps: []ir.Step{tt.step}}
			if tt.dag != nil {
				tt.dag(dag)
			}
			exists := tt.exists
			if exists == nil {
				exists = nothingExists
			}

			issues := findMissingCommands(dag, tt.shell, exists)

			require.Len(t, issues, len(tt.missing))
			for i, command := range tt.missing {
				assert.Equal(t, api.ValidationIssueSeverityWarning, issues[i].Severity)
				assert.Equal(t, api.ValidationIssueSourceHost, issues[i].Source)
				assert.Contains(t, issues[i].Message, command)
			}
		})
	}
}

// Each command of a step and each handler gets its own location.
func TestFindMissingCommandsLocation(t *testing.T) {
	t.Parallel()

	absolute := filepath.Join(t.TempDir(), "tool")
	handler := commandStep(absolute)
	handler.Name = "onExit"
	dag := &ir.DAG{
		Steps:     []ir.Step{commandStep("first"), commandStep("ls", absolute)},
		HandlerOn: ir.HandlerOn{Exit: &handler},
	}

	issues := findMissingCommands(dag, "/bin/sh", func(command string) bool { return command != absolute })

	require.Len(t, issues, 2)
	assert.Equal(t, "steps[1]", *issues[0].StepPath)
	assert.Equal(t, "steps[1].command[1]", *issues[0].FieldPath)
	assert.Equal(t, "s", *issues[0].StepName)
	assert.Equal(t, "handler_on.exit", *issues[1].StepPath)
	assert.Equal(t, "handler_on.exit.command", *issues[1].FieldPath)
	assert.Equal(t, "onExit", *issues[1].StepName)
}

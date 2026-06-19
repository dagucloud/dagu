// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package command

import "context"

type ShellCommandBuilderForTest struct {
	Dir                string
	Command            string
	Args               []string
	Shell              []string
	ShellCommandArgs   string
	ShellPackages      []string
	Script             string
	UserSpecifiedShell bool
}

func BuildShellCommandForTest(ctx context.Context, b ShellCommandBuilderForTest) ([]string, error) {
	builder := &shellCommandBuilder{
		Dir:                b.Dir,
		Command:            b.Command,
		Args:               b.Args,
		Shell:              b.Shell,
		ShellCommandArgs:   b.ShellCommandArgs,
		ShellPackages:      b.ShellPackages,
		Script:             b.Script,
		UserSpecifiedShell: b.UserSpecifiedShell,
	}
	cmd, err := builder.Build(ctx)
	if err != nil {
		return nil, err
	}
	return cmd.Args, nil
}

func ParseScriptShebangForTest(script string) (string, []string, error) {
	return parseScriptShebang(script)
}

func ResolveShebangExecutableForTest(ctx context.Context, command string) (string, error) {
	return resolveShebangExecutable(ctx, command)
}

func SetupScriptForExecutionForTest(workDir, script, command string, shell []string, userSpecifiedShell bool) (string, error) {
	return setupScriptForExecution(workDir, script, command, shell, userSpecifiedShell)
}

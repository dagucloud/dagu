// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// shellSyntaxChars are the characters that make the first word of a command
// something other than a literal executable: a variable, a substitution, a
// glob, an assignment, a quote, or an operator.
const shellSyntaxChars = "$`*?[]{}<>|&;()'\"!#=% \t"

// homePrefix starts a word that the shell expands to a home directory.
const homePrefix = "~"

// pathEnvName is the variable that the lookup of a bare command name reads.
const pathEnvName = "PATH"

// directShell is the shell value of a step that runs its command without a shell.
const directShell = "direct"

// posixShells are the shells whose built-ins and keywords shellWords lists.
var posixShells = []string{"sh", "bash", "zsh", "dash", "ksh", "ash"}

// shellWords are the built-ins and keywords of the POSIX shells. A shell runs
// them without an executable of the same name.
var shellWords = map[string]struct{}{}

func init() {
	words := ". : alias autoload bg bind break builtin caller case cd command compgen complete " +
		"compopt continue coproc declare dirs disown do done echo elif else emulate enable esac " +
		"eval exec exit export false fc fg fi for function getopts hash help history if in jobs " +
		"kill let local logout mapfile noglob popd print printf pushd pwd read readarray readonly " +
		"rehash repeat return select set setopt shift shopt source suspend test then time times " +
		"trap true type typeset ulimit umask unalias unset unsetopt until wait whence where while " +
		"zmodload"
	for word := range strings.FieldsSeq(words) {
		shellWords[word] = struct{}{}
	}
}

// hostCommandIssues returns one warning for each step command whose
// executable the server host does not have. It reports nothing when the
// server cannot know the answer: the caller may not edit the DAG, or the DAG
// runs on a worker.
func (a *API) hostCommandIssues(ctx context.Context, dag *ir.DAG) []api.ValidationIssue {
	if dag == nil {
		return nil
	}
	// The result reveals which paths exist on the server.
	if a.requireWorkspaceWrite(ctx, dagWorkspaceName(dag)) != nil {
		return nil
	}
	if dispatch.ShouldDispatchToCoordinator(dag, a.coordinatorCli != nil, a.defaultExecMode) {
		return nil
	}
	defaultShell := dag.Shell
	if defaultShell == "" {
		defaultShell = cmdutil.GetShellCommand(a.config.Core.DefaultShell)
	}
	return findMissingCommands(dag, defaultShell, func(command string) bool {
		_, ok := cmdutil.FindExecutable(command)
		return ok
	})
}

// findMissingCommands checks the commands of the local command steps of dag.
// defaultShell is the shell of a step that sets none. exists reports whether
// the host has an executable.
func findMissingCommands(dag *ir.DAG, defaultShell string, exists func(string) bool) []api.ValidationIssue {
	// A DAG can change the lookup of a bare name in ways that only a run shows.
	pathKnown := !setsPath(dag.Env) && dag.Tools == nil

	found := map[string]bool{}
	lookup := func(command string) bool {
		ok, seen := found[command]
		if !seen {
			ok = exists(command)
			found[command] = ok
		}
		return ok
	}

	var issues []api.ValidationIssue
	check := func(step *ir.Step, stepPath string) {
		if !runsOnHost(step) {
			return
		}
		shell := step.Shell
		if shell == "" {
			shell = defaultShell
		}
		bareNames := pathKnown && !setsPath(step.Env) && len(step.ShellPackages) == 0
		for i, entry := range step.Commands {
			if !checkable(entry.Command, shell, bareNames) || lookup(entry.Command) {
				continue
			}
			fieldPath := stepPath + ".command"
			if len(step.Commands) > 1 {
				fieldPath = fmt.Sprintf("%s[%d]", fieldPath, i)
			}
			issues = append(issues, api.ValidationIssue{
				Severity: api.ValidationIssueSeverityWarning,
				Source:   api.ValidationIssueSourceHost,
				// The command is printed as written: %q would double the
				// separators of a Windows path.
				Message:   fmt.Sprintf("step %q: command '%s' was not found on the server host", step.Name, entry.Command),
				StepName:  ptrOf(step.Name),
				StepPath:  ptrOf(stepPath),
				FieldPath: ptrOf(fieldPath),
			})
		}
	}

	for i := range dag.Steps {
		check(&dag.Steps[i], fmt.Sprintf("steps[%d]", i))
	}
	for _, h := range handlerEvents {
		if step := h.step(&dag.HandlerOn); step != nil {
			check(step, "handler_on."+h.event)
		}
	}
	return issues
}

// runsOnHost reports whether the step runs its commands as processes of the
// host. A script step is excluded: its command is an interpreter line that
// the shell or the shebang resolves.
func runsOnHost(step *ir.Step) bool {
	switch step.ExecutorConfig.Type {
	case "", "shell", "command":
	default:
		return false
	}
	return step.Container == nil && step.SubDAG == nil && step.Script == "" && len(step.WorkerSelector) == 0
}

// checkable reports whether the host file system decides if command can run.
// An absolute path always qualifies. A bare name qualifies when bareNames is
// true and the shell resolves it from PATH. A relative path depends on the
// working directory of the run and never qualifies.
func checkable(command, shell string, bareNames bool) bool {
	if command == "" || strings.HasPrefix(command, homePrefix) || strings.ContainsAny(command, shellSyntaxChars) {
		return false
	}
	if filepath.IsAbs(command) {
		return true
	}
	if !bareNames || strings.ContainsAny(command, `/\`) {
		return false
	}
	name := shellName(shell)
	if name == directShell {
		return true
	}
	if !slices.Contains(posixShells, name) {
		return false
	}
	_, isShellWord := shellWords[command]
	return !isShellWord
}

// shellName returns the program name of a shell command line, such as "bash"
// for "/bin/bash -e".
func shellName(shell string) string {
	program, _, _ := strings.Cut(strings.TrimSpace(shell), " ")
	name := strings.ToLower(filepath.Base(strings.ReplaceAll(program, `\`, "/")))
	return strings.TrimSuffix(name, ".exe")
}

// setsPath reports whether a list of KEY=VALUE entries sets PATH.
func setsPath(env []string) bool {
	return slices.ContainsFunc(env, func(entry string) bool {
		key, _, _ := strings.Cut(entry, "=")
		return strings.EqualFold(strings.TrimSpace(key), pathEnvName)
	})
}

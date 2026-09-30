// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/ir"
	dagutools "github.com/dagucloud/dagu/v2/internal/tools"
)

// warnDryRunStep logs the checkDryRunStep result as a warning. A missing
// executable never fails a dry run: the real run may execute on another host
// (a distributed worker, a server rather than a CI checkout), and an upstream
// step may create or install the executable first.
func warnDryRunStep(ctx context.Context, node *Node) {
	if err := checkDryRunStep(ctx, node.Step()); err != nil {
		logger.Warn(ctx, "Dry run: step may fail on this host", tag.Error(err))
	}
}

// checkDryRunStep reports the executable-access failures a local command step
// would hit if it ran on this host now — a shell that is not on PATH, a
// command name that does not resolve, or a command path that is not
// executable — without running the step. Steps whose executor runs off the
// host (containers, SSH, remote jobs) are skipped because their commands
// resolve in an environment the dry run cannot observe.
func checkDryRunStep(ctx context.Context, step ir.Step) error {
	caps := registry.ExecutorCapabilitiesFor(step.ExecutorConfig.Type)
	if caps.CommandContext == nil {
		return nil
	}
	command := caps.CommandContext(ctx, step)
	if command.Target != cmnvalue.CommandTargetLocal {
		return nil
	}

	env := GetEnv(ctx)
	envs := env.AllEnvs()

	direct := true
	unixShell := false
	if len(command.Shell) > 0 && !isDirectShellName(command.Shell[0]) {
		direct = false
		shell := command.Shell[0]
		// The executor resolves the shell with cmdutil.ResolveExecutable, so
		// the lookup uses the dagu process PATH, not the step environment.
		if _, ok := cmdutil.FindExecutable(shell); !ok {
			return fmt.Errorf("field 'shell': shell %q not found on this host", shell)
		}
		if cmdutil.IsNixShell(shell) {
			// Commands may be supplied by shell_packages instead of PATH.
			return nil
		}
		unixShell = cmdutil.IsUnixLikeShell(shell)
	}

	// Command names can only be checked when the step execs them directly or
	// through a Unix-like shell; other shells resolve names the host PATH
	// cannot see (builtins, cmdlets, aliases).
	if !direct && !unixShell {
		return nil
	}
	commands := step.Commands
	if len(commands) == 0 && step.Command != "" {
		commands = []ir.CommandEntry{{Command: step.Command, Args: step.Args, CmdWithArgs: step.CmdWithArgs}}
	}
	for i, entry := range commands {
		fieldPath := commandEntryFieldPath(len(commands), i)
		err := checkDryRunCommand(ctx, entry, command, env, envs, fieldPath,
			direct || step.Script != "")
		if err != nil {
			return err
		}
	}
	return nil
}

// checkDryRunCommand checks one command entry. noShell is true when the
// command is exec'd without a shell: its name resolves through the dagu
// process PATH, as exec.Command does, and shell builtins cannot satisfy it.
// Otherwise a Unix-like shell resolves the name through the step PATH.
func checkDryRunCommand(
	ctx context.Context,
	entry ir.CommandEntry,
	command cmnvalue.CommandContext,
	env Env,
	envs []string,
	fieldPath string,
	noShell bool,
) error {
	name := entry.Command
	if name == "" {
		return nil
	}
	// Evaluate references the same way execution does; names that cannot be
	// resolved statically are left to the real run rather than guessed.
	if evaluated, err := resolveRuntimeString(
		ctx, name, cmnvalue.DirectCommandField(fieldPath, command),
	); err == nil {
		name = evaluated
	}
	if !dryCheckableName(name) {
		return nil
	}

	if strings.HasPrefix(name, "~/") {
		home, ok := cmdutil.LookupEnv(envs, "HOME")
		if !ok {
			home, _ = os.UserHomeDir()
		}
		if home == "" {
			return nil
		}
		name = filepath.Join(home, name[2:])
	} else if strings.HasPrefix(name, "~") {
		return nil
	}
	if strings.ContainsAny(name, `/\`) {
		// Path-form command: the process execs the file relative to the
		// step's working directory and requires the executable bit.
		path := name
		if !filepath.IsAbs(path) {
			path = filepath.Join(env.WorkingDir, path)
		}
		if !cmdutil.IsExecutableFileInEnv(path, envs) {
			if _, err := os.Stat(path); err != nil {
				return fmt.Errorf("field '%s': command %q: %w", fieldPath, entry.Command, err)
			}
			return fmt.Errorf("field '%s': command %q: not an executable file", fieldPath, entry.Command)
		}
		return nil
	}

	var found bool
	if noShell {
		_, found = cmdutil.FindExecutable(name)
	} else {
		_, err := cmdutil.LookPathInEnvDir(name, envs, env.WorkingDir)
		found = err == nil || shellBuiltins[name]
	}
	if found || dryRunToolCommand(env, name) {
		return nil
	}
	return fmt.Errorf("field '%s': command %q not found on this host", fieldPath, entry.Command)
}

// dryRunToolCommand reports whether name is provided by the resolved Dagu
// tools manifest, which direct execution consults before PATH.
func dryRunToolCommand(env Env, name string) bool {
	manifestPath := env.UserEnvsMap()[dagutools.EnvManifest]
	if manifestPath == "" {
		return false
	}
	manifest, err := dagutools.ReadManifest(manifestPath)
	if err != nil {
		return false
	}
	cmd, ok := manifest.Commands[name]
	return ok && cmd.Path != ""
}

// dryCheckableName reports whether name is a plain executable name or path
// worth checking. Names containing shell syntax or unresolved references are
// skipped: they may be legal shell or resolve only at run time.
func dryCheckableName(name string) bool {
	return name != "" && !strings.ContainsAny(name, " \t\n\"'`$(){}[]<>|&;=*?#!")
}

func isDirectShellName(name string) bool {
	return strings.EqualFold(strings.TrimSuffix(filepath.Base(name), ".exe"), "direct")
}

// shellBuiltins are commands a Unix-like shell (sh, bash, zsh, ksh) resolves
// internally, so a PATH lookup failure for one of them does not mean the step
// cannot run.
var shellBuiltins = map[string]bool{
	"!": true, ".": true, ":": true, "[": true, "[[": true,
	"alias": true, "bg": true, "break": true, "builtin": true,
	"caller": true, "case": true, "cd": true, "command": true,
	"continue": true, "declare": true, "dirs": true, "disown": true,
	"do": true, "done": true, "echo": true, "elif": true, "else": true,
	"esac": true, "eval": true, "exec": true, "exit": true, "export": true,
	"false": true, "fc": true, "fg": true, "fi": true, "for": true,
	"function": true, "getopts": true, "hash": true, "help": true,
	"history": true, "if": true, "in": true, "jobs": true, "kill": true,
	"let": true, "local": true, "logout": true, "mapfile": true,
	"popd": true, "printf": true, "pushd": true, "pwd": true, "read": true,
	"readarray": true, "readonly": true, "return": true, "select": true,
	"set": true, "shift": true, "shopt": true, "source": true,
	"suspend": true, "test": true, "then": true, "time": true,
	"times": true, "trap": true, "true": true, "type": true,
	"typeset": true, "ulimit": true, "umask": true, "unalias": true,
	"unset": true, "until": true, "wait": true, "while": true,
	// bash
	"bind": true, "compgen": true, "complete": true, "compopt": true,
	"coproc": true, "disable": true, "enable": true,
	// zsh and ksh
	"autoload": true, "bindkey": true, "emulate": true, "float": true,
	"functions": true, "integer": true, "noglob": true, "print": true,
	"rehash": true, "repeat": true, "setopt": true, "unfunction": true,
	"unsetopt": true, "vared": true, "whence": true, "where": true,
	"which": true, "zle": true, "zmodload": true, "zparseopts": true,
	"zstyle": true,
}

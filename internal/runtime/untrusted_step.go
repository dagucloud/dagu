// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"fmt"
	"os"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/dagucloud/dagu/v2/internal/runtime/untrusted"
)

// beginUntrustedStep prepares an untrusted step (Spec 103) before the setup of
// its executor, so that the fields of the step resolve with the HOME, the
// TMPDIR and the output file of the process. It returns ctx unchanged and nil
// for a trusted step.
func beginUntrustedStep(ctx context.Context, node *Node) (context.Context, *untrusted.Step, error) {
	step := node.Step()
	if !step.IsUntrusted() {
		return ctx, nil, nil
	}
	env, ok := LookupEnv(ctx)
	if !ok {
		env = NewEnv(ctx, step)
	}
	session, err := untrusted.Begin(ctx, untrusted.Request{
		StepName:    step.Name,
		Config:      config.GetConfig(ctx).Untrusted,
		WorkDir:     GetDAGContext(ctx).RunWorkDir,
		ManagedRoot: config.GetConfig(ctx).Paths.DAGRunWorkDir,
		WorkingDir:  env.WorkingDir,
		OutputLimit: maxOutputSize(ctx),
		LockDir:     config.GetConfig(ctx).Paths.DataDir,
		Log:         node.outputs.stderrLog(),
	})
	if err != nil {
		return ctx, nil, err
	}
	ctx = untrusted.WithStep(ctx, session)
	if home := session.Home(); home != "" {
		env.Scope = env.Scope.WithEntries(map[string]string{"HOME": home, "TMPDIR": home}, cmnvalue.EnvSourceStepEnv)
		ctx = WithEnv(ctx, env)
	}
	return ctx, session, nil
}

// withTrustedHome gives a trusted step of an isolated run a private HOME when
// the restricted user can write or replace the HOME of the worker or an XDG
// folder (Spec 103, "Protections for later trusted steps"). Then a file that an untrusted step planted
// there, such as a git config with hooks, does not reach the trusted step. The
// caller calls the returned function at the end of the step, which removes the
// private HOME.
func withTrustedHome(ctx context.Context, node *Node) (context.Context, func(), error) {
	cfg := config.GetConfig(ctx).Untrusted
	if !cfg.Enabled {
		return ctx, func() {}, nil
	}
	env, ok := LookupEnv(ctx)
	if !ok {
		env = NewEnv(ctx, node.Step())
	}
	home, _ := env.Scope.Get("HOME")
	entries, remove, err := untrusted.TrustedHome(cfg.UID, cfg.GID, GetDAGContext(ctx).RunWorkDir, env.WorkingDir, home, env.Scope.Get)
	if err != nil {
		return ctx, nil, fmt.Errorf("step %q: make a private HOME for a trusted step: %w", node.Step().Name, err)
	}
	if entries == nil {
		return ctx, func() {}, nil
	}
	env.Scope = env.Scope.WithEntries(entries, cmnvalue.EnvSourceStepEnv)
	return WithEnv(ctx, env), func() {
		if err := remove(); err != nil {
			logger.Warn(ctx, "Failed to remove the private HOME of a trusted step", tag.Step(node.Name()), tag.Error(err))
		}
	}, nil
}

// untrustedRun reports whether the run of ctx has an untrusted step.
func untrustedRun(ctx context.Context) bool {
	rCtx, ok := LookupDAGContext(ctx)
	return ok && rCtx.HasUntrustedStep
}

// isolatedRun reports whether the run has an untrusted step and the
// configuration has a restricted user.
func isolatedRun(ctx context.Context) bool {
	return untrustedRun(ctx) && config.GetConfig(ctx).Untrusted.Enabled
}

// gatedStep reports whether the step takes a place in the gate of its run. A
// foreach step does not: its item bodies are steps of the run, which take
// their own places. A human task runs no process. A call or parallel step
// takes a place for as long as its child runs run, since a child run has a
// gate of its own.
func gatedStep(step ir.Step) bool {
	return step.Foreach == nil && step.HumanTask == nil
}

// enterStepGate takes the place of the node in the gate of its run (Spec 103,
// "Isolation", rule 1). In a run with an isolated untrusted step, an
// untrusted step holds the run alone, and a trusted step waits for its end.
// It returns the function that gives the place back. An error is the end of
// ctx, the stop of the isolation of the process, or a work directory that was
// not given back safely.
func enterStepGate(ctx context.Context, node *Node) (func(), error) {
	if !isolatedRun(ctx) {
		return func() {}, checkWorkDir(ctx)
	}
	if !gatedStep(node.Step()) {
		return func() {}, nil
	}
	if err := untrusted.Poisoned(); err != nil {
		return nil, err
	}
	gate := GetDAGContext(ctx).StepGate
	log := node.outputs.stderrLog()
	var leave func()
	var err error
	if node.Step().IsUntrusted() {
		leave, err = unlessPoisoned(gate.Write(ctx, func() { untrusted.WriteLine(log, untrusted.WaitingRunLine) }))
	} else {
		leave, err = unlessPoisoned(gate.Read(ctx, func() { untrusted.WriteLine(log, untrusted.WaitingTrustedLine) }))
	}
	return unlessUnsafeWorkDir(ctx, leave, err)
}

// enterTrustedGate takes a place of a trusted reader for the trusted code of a
// node, such as its preconditions, also for an untrusted step.
func enterTrustedGate(ctx context.Context, node *Node) (func(), error) {
	if !isolatedRun(ctx) {
		return func() {}, checkWorkDir(ctx)
	}
	if err := untrusted.Poisoned(); err != nil {
		return nil, err
	}
	log := node.outputs.stderrLog()
	leave, err := unlessPoisoned(GetDAGContext(ctx).StepGate.Read(ctx, func() { untrusted.WriteLine(log, untrusted.WaitingTrustedLine) }))
	return unlessUnsafeWorkDir(ctx, leave, err)
}

// unlessPoisoned gives back a place of the gate when the isolation of the
// process stopped while the node waited: the step that held the gate can have
// failed its End.
func unlessPoisoned(leave func(), err error) (func(), error) {
	if err != nil {
		return nil, err
	}
	if err := untrusted.Poisoned(); err != nil {
		leave()
		return nil, err
	}
	return leave, nil
}

// unlessUnsafeWorkDir gives back a place of the gate when the work directory
// of the run was not given back safely.
func unlessUnsafeWorkDir(ctx context.Context, leave func(), err error) (func(), error) {
	if err != nil {
		return nil, err
	}
	if err := checkWorkDir(ctx); err != nil {
		leave()
		return nil, err
	}
	return leave, nil
}

// checkWorkDir fails a step of a run with an untrusted step while the work
// directory of the run is not known to be safe: an untrusted step got it and no
// restore made it safe again (Spec 103, "Isolation"). A step of an isolated
// run checks once it holds its place in the gate, after the end of the
// untrusted step that held the run.
func checkWorkDir(ctx context.Context) error {
	if !untrustedRun(ctx) {
		return nil
	}
	return untrusted.CheckWorkDir(GetDAGContext(ctx).RunWorkDir)
}

// checkUntrustedExecutor fails an untrusted step whose executor does not start
// its processes through untrusted.StartManagedProcess.
func checkUntrustedExecutor(ctx context.Context, node *Node, cmd executor.Executor) error {
	if untrusted.FromContext(ctx) == nil {
		return nil
	}
	if _, ok := cmd.(executor.UntrustedRunner); !ok {
		return fmt.Errorf("untrusted step %q: the action of the step cannot run as an untrusted step", node.Step().Name)
	}
	return nil
}

// OpenStepFile opens a file that Dagu reads or writes for a step, such as a
// redirect of stdout or the file of stdin. In a run with an untrusted step, a
// path in the work directory of the run cannot pass a symbolic link, which an
// untrusted step could have made to a file of the worker (Spec 103).
func OpenStepFile(ctx context.Context, path string, flags int, perm os.FileMode) (*os.File, error) {
	rCtx, ok := LookupDAGContext(ctx)
	if !ok || !rCtx.HasUntrustedStep {
		return os.OpenFile(path, flags, perm) // #nosec G304 -- the path comes from the workflow.
	}
	return untrusted.OpenWorkFile(rCtx.RunWorkDir, path, flags, perm)
}

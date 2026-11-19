// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"fmt"
	"os"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/dagucloud/dagu/v2/internal/runtime/untrusted"
)

// beginUntrustedStep prepares an untrusted step (Spec 092) before the setup of
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
// the restricted user can write the HOME of the worker (Spec 092, "Trusted
// steps after an untrusted step"). Then a file that an untrusted step planted
// there, such as a git config with hooks, does not reach the trusted step.
func withTrustedHome(ctx context.Context, node *Node) (context.Context, error) {
	cfg := config.GetConfig(ctx).Untrusted
	if !cfg.Enabled {
		return ctx, nil
	}
	env, ok := LookupEnv(ctx)
	if !ok {
		env = NewEnv(ctx, node.Step())
	}
	home, _ := env.Scope.Get("HOME")
	entries, err := untrusted.TrustedHome(cfg.UID, cfg.GID, home, env.Scope.Get)
	if err != nil {
		return ctx, fmt.Errorf("step %q: make a private HOME for a trusted step: %w", node.Step().Name, err)
	}
	if entries == nil {
		return ctx, nil
	}
	env.Scope = env.Scope.WithEntries(entries, cmnvalue.EnvSourceStepEnv)
	return WithEnv(ctx, env), nil
}

// isolatedRun reports whether the run has an untrusted step and the
// configuration has a restricted user.
func isolatedRun(ctx context.Context) bool {
	return config.GetConfig(ctx).Untrusted.Enabled && GetDAGContext(ctx).DAG.HasUntrustedStep()
}

// gatedStep reports whether the step takes a place in the gate of its run. A
// step that runs a child run or other steps (call, parallel, foreach) and a
// human task do not: the steps that they run take their own places.
func gatedStep(step ir.Step) bool {
	return step.Foreach == nil && step.SubDAG == nil && step.Parallel == nil && step.HumanTask == nil
}

// enterStepGate takes the place of the node in the gate of its run (Spec 092,
// "Isolation", "One at a time"). In a run with an isolated untrusted step, an
// untrusted step holds the run alone, and a trusted step waits for its end.
// It returns the function that gives the place back. An error is the end of
// ctx, or the stop of the isolation of the process.
func enterStepGate(ctx context.Context, node *Node) (func(), error) {
	if !isolatedRun(ctx) || !gatedStep(node.Step()) {
		return func() {}, nil
	}
	if err := untrusted.Poisoned(); err != nil {
		return nil, err
	}
	gate := GetDAGContext(ctx).StepGate
	log := node.outputs.stderrLog()
	if node.Step().IsUntrusted() {
		return gate.Write(ctx, func() { untrusted.WriteLine(log, untrusted.WaitingRunLine) })
	}
	return gate.Read(ctx, func() { untrusted.WriteLine(log, untrusted.WaitingTrustedLine) })
}

// enterTrustedGate takes a place of a trusted reader for the trusted code of a
// node, such as its preconditions, also for an untrusted step.
func enterTrustedGate(ctx context.Context, node *Node) (func(), error) {
	if !isolatedRun(ctx) {
		return func() {}, nil
	}
	if err := untrusted.Poisoned(); err != nil {
		return nil, err
	}
	log := node.outputs.stderrLog()
	return GetDAGContext(ctx).StepGate.Read(ctx, func() { untrusted.WriteLine(log, untrusted.WaitingTrustedLine) })
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
// untrusted step could have made to a file of the worker (Spec 092).
func OpenStepFile(ctx context.Context, path string, flags int, perm os.FileMode) (*os.File, error) {
	rCtx := GetDAGContext(ctx)
	if !rCtx.DAG.HasUntrustedStep() {
		return os.OpenFile(path, flags, perm) // #nosec G304 -- the path comes from the workflow.
	}
	return untrusted.OpenWorkFile(rCtx.RunWorkDir, path, flags, perm)
}

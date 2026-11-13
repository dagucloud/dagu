// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package untrusted runs an untrusted step (Spec 092, "Untrusted steps").
//
// In each process, the process of an untrusted step gets no secret and no
// variable whose name starts with DAGU_, except DAGU_OUTPUT_FILE. When the
// configuration sets untrusted.uid and untrusted.gid, the step also runs with
// isolation: one untrusted step at a time, as the restricted user, with its own
// HOME, after Dagu gives the work directory of the run to the restricted user.
// At the end of the step, Dagu stops each process of the restricted user and
// gives the work directory back to the user of the worker.
//
// Begin prepares the step and End ends it. Between the two calls, each
// process of the step starts through StartManagedProcess.
package untrusted

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/runenv"
)

// ErrRunAborted marks an error of an untrusted step after which the run stops:
// a process of the restricted user survives, or a git folder changed. Dagu
// cannot give a safe work directory to a later step.
var ErrRunAborted = errors.New("the run stops")

// IsolationOffLine is the line of the step log when the configuration sets
// untrusted.isolation to off.
const IsolationOffLine = "untrusted: isolation is off: the key untrusted.isolation is off"

// ReasonIsolationRequired is the failure reason of an untrusted step that finds
// no restricted user in the configuration, while untrusted.isolation is on.
const ReasonIsolationRequired = "isolation_required"

// ErrIsolationRequired is the error of an untrusted step that cannot start
// because the configuration has no restricted user and does not turn the
// isolation off. The step fails before it starts.
var ErrIsolationRequired = errors.New(ReasonIsolationRequired +
	": the configuration sets no untrusted.uid and no untrusted.gid, and untrusted.isolation is on:" +
	" set untrusted.uid and untrusted.gid, or set untrusted.isolation to off for development")

// WaitingLine is the line of the step log while the step waits for the end of
// another untrusted step.
const WaitingLine = "untrusted: waiting for the restricted user"

// WaitingRunLine is the line of the step log while an isolated untrusted step
// waits for the end of the other running steps of its run.
const WaitingRunLine = "untrusted: waiting for the end of the other steps of the run"

// WaitingTrustedLine is the line of the step log while a step waits for the end
// of an isolated untrusted step of its run.
const WaitingTrustedLine = "untrusted: waiting for the end of the untrusted step"

// WriteLine writes one line to a step log.
func WriteLine(w io.Writer, line string) { writeLine(w, line) }

// systemVarPrefix is the prefix of the variables that an untrusted process does
// not get.
const systemVarPrefix = "DAGU_"

// Request describes one untrusted step for Begin.
type Request struct {
	// StepName names the step in the errors.
	StepName string
	// Config is the block untrusted of the configuration.
	Config config.UntrustedConfig
	// WorkDir is the work directory of the run (DAG_RUN_WORK_DIR).
	WorkDir string
	// ManagedRoot is the root of the work directories that Dagu makes for
	// its runs (paths.dag_run_work_dir). A folder of a run below it can get
	// the bit x for others during the step, so that the restricted user can
	// reach the work directory.
	ManagedRoot string
	// WorkingDir is the working directory of the step. It must be in WorkDir.
	WorkingDir string
	// OutputLimit is the size limit of the output file.
	OutputLimit int64
	// LockDir is the data directory of Dagu. Its folder untrusted holds the
	// lock file of the host when /run/dagu is not available.
	LockDir string
	// Log is the step log for the lines of this package.
	Log io.Writer
}

// Step is one untrusted step between Begin and End.
type Step struct {
	name string
	iso  *isolation

	endOnce sync.Once
	endErr  error
}

type stepKey struct{}

// ancestorKey marks a context inside an untrusted step, also in a child run
// that the step starts, such as a DAG tool of the tool bridge.
type ancestorKey struct{}

// WithStep returns ctx with the untrusted step.
func WithStep(ctx context.Context, step *Step) context.Context {
	ctx = context.WithValue(ctx, stepKey{}, step)
	if step != nil {
		ctx = context.WithValue(ctx, ancestorKey{}, step.name)
	}
	return ctx
}

// ForChildRun returns ctx for a child run that an untrusted step starts, such
// as a DAG tool of the tool bridge. The child run is trusted (D-050): its
// processes do not get the rules of the untrusted step. An untrusted step of
// the child run cannot get isolation, because the parent step holds the
// restricted user.
func ForChildRun(ctx context.Context) context.Context {
	if FromContext(ctx) == nil {
		return ctx
	}
	ctx = context.WithValue(ctx, childRunKeyType{}, true)
	return context.WithValue(ctx, stepKey{}, (*Step)(nil))
}

// FromContext returns the untrusted step of ctx, or nil for a trusted step.
func FromContext(ctx context.Context) *Step {
	if ctx == nil {
		return nil
	}
	step, _ := ctx.Value(stepKey{}).(*Step)
	return step
}

// Begin prepares an untrusted step. Without a restricted user in the
// configuration, it fails the step with ErrIsolationRequired, unless the
// configuration sets untrusted.isolation to off: then it writes IsolationOffLine
// to the step log and the step runs without isolation. With a restricted user, it waits
// for the end of the previous untrusted step, checks the place of the step,
// and gives the work directory to the restricted user. A failure fails the
// step; the error names the action and the path.
func Begin(ctx context.Context, req Request) (*Step, error) {
	step := &Step{name: req.StepName}
	if !req.Config.Enabled {
		if !req.Config.IsolationOff {
			return nil, fmt.Errorf("untrusted step %q: %w", req.StepName, ErrIsolationRequired)
		}
		writeLine(req.Log, IsolationOffLine)
		return step, nil
	}
	if parent, ok := ctx.Value(ancestorKey{}).(string); ok {
		// The parent step holds the restricted user until it ends: a wait for it
		// would never end.
		return nil, fmt.Errorf("untrusted step %q cannot run inside the untrusted step %q", req.StepName, parent)
	}
	return isolate(ctx, req, step)
}

// Isolated reports whether the step runs as the restricted user.
func (s *Step) Isolated() bool {
	return s != nil && s.iso != nil
}

// Home returns the HOME and TMPDIR of the process of an isolated step, or ""
// without isolation.
func (s *Step) Home() string {
	if !s.Isolated() {
		return ""
	}
	return s.iso.home
}

// OutputFile returns the file of DAGU_OUTPUT_FILE that the process of the step
// writes. With isolation, it is a file of the restricted user, and End copies
// it to target before Dagu reads the outputs. Without isolation, it is target.
func (s *Step) OutputFile(target string) string {
	if !s.Isolated() || target == "" {
		return target
	}
	return s.iso.setOutputTarget(target)
}

// End ends the step after a success, a failure, a timeout or a stop. With
// isolation, it stops each process of the restricted user, gives the work
// directory back to the user of the worker, and copies the output file. It
// runs once; a later call returns the first result.
func (s *Step) End(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.endOnce.Do(func() {
		if s.iso != nil {
			s.endErr = s.iso.end(context.WithoutCancel(ctx))
		}
	})
	return s.endErr
}

// StartManagedProcess starts cmd as cmdutil.StartManagedProcess does. For an
// untrusted step in ctx, it first applies the rules of the step to cmd: the
// variables, and with isolation the user, the group, HOME, TMPDIR, the output
// file and the place of the working directory.
func StartManagedProcess(ctx context.Context, cmd *exec.Cmd) (*cmdutil.ManagedProcess, error) {
	step := FromContext(ctx)
	if step == nil || cmd == nil {
		// A nil Env inherits the variables of Dagu; it stays nil.
		if cmd != nil && cmd.Env != nil && gitSafetyFrom(ctx) {
			cmd.Env = WithGitSafety(cmd.Env)
		}
		return cmdutil.StartManagedProcess(cmd)
	}
	if err := step.prepare(cmd); err != nil {
		return nil, err
	}
	process, err := cmdutil.StartManagedProcess(cmd)
	if err != nil {
		return nil, err
	}
	if step.iso != nil {
		step.iso.started(process.PID())
	}
	return process, nil
}

// prepare applies the rules of the step to cmd before it starts.
func (s *Step) prepare(cmd *exec.Cmd) error {
	env := FilterEnv(cmd.Env)
	if s.iso == nil {
		cmd.Env = env
		return nil
	}
	return s.iso.prepare(cmd, env)
}

// FilterEnv returns the variables without those whose names start with DAGU_,
// except DAGU_OUTPUT_FILE. It never returns nil, so that a process never
// inherits the variables of Dagu.
func FilterEnv(env []string) []string {
	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(strings.ToUpper(key), systemVarPrefix) && key != runenv.EnvKeyDAGUOutputFile {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

// setEnv returns env with key set to value, once.
func setEnv(env []string, key, value string) []string {
	result := make([]string, 0, len(env)+1)
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		if name == key {
			continue
		}
		result = append(result, entry)
	}
	return append(result, key+"="+value)
}

// WriteFile writes a file of the attempt that only the restricted user can
// read, such as the config of the tool bridge, and returns its path. The file
// belongs to the restricted user and has the mode 0400 (Spec 092, "Files of the
// attempt"). Without isolation, it returns an error: the caller keeps its own
// file.
func (s *Step) WriteFile(name string, data []byte) (string, error) {
	if !s.Isolated() {
		return "", errors.New("untrusted: the step has no isolation")
	}
	if name == "" || filepath.Base(name) != name || name == "." || name == ".." {
		return "", fmt.Errorf("untrusted step %q: invalid file name %q", s.name, name)
	}
	return s.iso.writeOwnedFile(name, data)
}

// ShareReadOnly lets the restricted user read the file or the folder at path
// and its content, and not write it. Without isolation, it does nothing.
func (s *Step) ShareReadOnly(path string) error {
	if !s.Isolated() {
		return nil
	}
	return shareReadOnly(s.name, path)
}

func writeLine(w io.Writer, line string) {
	if w == nil {
		return
	}
	_, _ = io.WriteString(w, line+"\n")
}

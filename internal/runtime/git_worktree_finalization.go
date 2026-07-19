// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
)

func cloneGitWorktreeFinalization(in *exec.GitWorktreeFinalization) *exec.GitWorktreeFinalization {
	if in == nil {
		return nil
	}
	out := *in
	out.Cleanups = append([]exec.GitWorktreeCleanup(nil), in.Cleanups...)
	return &out
}

func (r *Runner) setGitWorktreeCleanup(cleanup exec.GitWorktreeCleanup) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.gitWorktreeFinalization != nil {
		for i, existing := range r.gitWorktreeFinalization.Cleanups {
			if !sameGitWorktreeCleanupTarget(existing, cleanup) {
				continue
			}
			if cleanup.Policy == core.GitWorktreeCleanupNever {
				cleanups := r.gitWorktreeFinalization.Cleanups
				r.gitWorktreeFinalization.Cleanups = append(cleanups[:i], cleanups[i+1:]...)
				if len(r.gitWorktreeFinalization.Cleanups) == 0 {
					r.gitWorktreeFinalization = nil
				}
			} else {
				r.gitWorktreeFinalization.Cleanups[i] = cleanup
			}
			return nil
		}
	}
	if cleanup.Policy == core.GitWorktreeCleanupNever {
		return nil
	}
	if r.gitWorktreeFinalization == nil {
		r.gitWorktreeFinalization = &exec.GitWorktreeFinalization{}
	}
	r.gitWorktreeFinalization.Cleanups = append(r.gitWorktreeFinalization.Cleanups, cleanup)
	return nil
}

func sameGitWorktreeCleanupTarget(left, right exec.GitWorktreeCleanup) bool {
	return left.CommonDir == right.CommonDir && left.Path == right.Path && left.Branch == right.Branch
}

// GitWorktreeFinalization returns a snapshot of run-owned cleanup state.
func (r *Runner) GitWorktreeFinalization() *exec.GitWorktreeFinalization {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return cloneGitWorktreeFinalization(r.gitWorktreeFinalization)
}

// HasGitWorktreeCleanups reports whether the run owns worktrees awaiting a terminal decision.
func (r *Runner) HasGitWorktreeCleanups() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.gitWorktreeFinalization != nil && len(r.gitWorktreeFinalization.Cleanups) > 0
}

// HasPendingGitWorktreeFinalization reports whether finalization was interrupted.
func (r *Runner) HasPendingGitWorktreeFinalization() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.gitWorktreeFinalization != nil &&
		r.gitWorktreeFinalization.Status != core.NotStarted &&
		len(r.gitWorktreeFinalization.Cleanups) > 0
}

// BeginGitWorktreeFinalization selects cleanup obligations for a terminal status.
func (r *Runner) BeginGitWorktreeFinalization(status core.Status, statusError string) []exec.GitWorktreeCleanup {
	if status == core.Failed && !r.shouldRunFailureHandler(status) {
		return nil
	}
	if !gitWorktreeTerminalStatus(status) {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.gitWorktreeFinalization == nil {
		return nil
	}
	if r.gitWorktreeFinalization.Status != core.NotStarted {
		status = r.gitWorktreeFinalization.Status
	} else {
		r.gitWorktreeFinalization.Status = status
		r.gitWorktreeFinalization.Error = statusError
	}

	eligible := make([]exec.GitWorktreeCleanup, 0, len(r.gitWorktreeFinalization.Cleanups))
	for _, cleanup := range r.gitWorktreeFinalization.Cleanups {
		if cleanup.Policy == core.GitWorktreeCleanupOnFinish ||
			(cleanup.Policy == core.GitWorktreeCleanupOnSuccess && status.IsSuccess()) {
			eligible = append(eligible, cleanup)
		}
	}
	r.gitWorktreeFinalization.Cleanups = eligible
	if len(eligible) == 0 {
		r.gitWorktreeFinalization = nil
		return nil
	}
	return append([]exec.GitWorktreeCleanup(nil), eligible...)
}

// CompleteGitWorktreeCleanup removes a successfully finalized obligation.
func (r *Runner) CompleteGitWorktreeCleanup(completed exec.GitWorktreeCleanup) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.gitWorktreeFinalization == nil {
		return
	}
	cleanups := r.gitWorktreeFinalization.Cleanups
	for i, cleanup := range cleanups {
		if sameGitWorktreeCleanupTarget(cleanup, completed) {
			r.gitWorktreeFinalization.Cleanups = append(cleanups[:i], cleanups[i+1:]...)
			break
		}
	}
}

// EndGitWorktreeFinalization closes the current finalization phase.
func (r *Runner) EndGitWorktreeFinalization() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.gitWorktreeFinalization == nil {
		return
	}
	if len(r.gitWorktreeFinalization.Cleanups) == 0 {
		r.gitWorktreeFinalization = nil
		return
	}
	r.gitWorktreeFinalization.Status = core.NotStarted
	r.gitWorktreeFinalization.Error = ""
}

func gitWorktreeTerminalStatus(status core.Status) bool {
	switch status {
	case core.Succeeded, core.PartiallySucceeded, core.Failed, core.Aborted, core.Rejected:
		return true
	case core.NotStarted, core.Running, core.Queued, core.Waiting:
		return false
	default:
		return false
	}
}

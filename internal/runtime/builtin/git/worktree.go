// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package git

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	osExec "os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gofrs/flock"
)

const (
	worktreeLockFile       = "dagu-worktree.lock"
	failedAddRepairTimeout = 5 * time.Second
)

var commitHashPattern = regexp.MustCompile(`^[0-9a-fA-F]{7,64}$`)

type repository struct {
	root      string
	commonDir string
}

type worktreeRegistration struct {
	path    string
	head    string
	branch  string
	primary bool
	bare    bool
}

func (e *executorImpl) runWorktree(ctx context.Context) error {
	repo, err := e.discoverRepository(ctx)
	if err != nil {
		return fmt.Errorf("git %s: %w", e.op, err)
	}
	if err := e.lockRepository(ctx, repo.commonDir); err != nil {
		return fmt.Errorf("git %s: %w", e.op, err)
	}

	var operationErr error
	switch e.op {
	case opWorktreeAdd:
		operationErr = e.runWorktreeAdd(ctx, repo)
	case opWorktreeRemove:
		operationErr = e.runWorktreeRemove(ctx, repo)
	default:
		return fmt.Errorf("git: unsupported operation %q", e.op)
	}
	if operationErr != nil {
		return fmt.Errorf("repository %q: %w", repo.root, operationErr)
	}
	return nil
}

func (e *executorImpl) discoverRepository(ctx context.Context) (repository, error) {
	workDir := e.workDir
	if workDir == "" {
		var err error
		workDir, err = os.Getwd()
		if err != nil {
			return repository{}, fmt.Errorf("resolve working directory: %w", err)
		}
	}
	info, err := os.Stat(workDir)
	if err != nil {
		return repository{}, fmt.Errorf("working directory %q: %w", workDir, err)
	}
	if !info.IsDir() {
		return repository{}, fmt.Errorf("working directory %q is not a directory", workDir)
	}

	bareText, err := e.gitOutput(ctx, workDir, "rev-parse", "--is-bare-repository")
	if err != nil {
		return repository{}, fmt.Errorf("discover repository from %q: %w", workDir, err)
	}
	bare := strings.TrimSpace(bareText) == "true"
	rootArg := "--show-toplevel"
	if bare {
		rootArg = "--absolute-git-dir"
	}
	root, err := e.gitOutput(ctx, workDir, "rev-parse", rootArg)
	if err != nil {
		return repository{}, fmt.Errorf("resolve repository root: %w", err)
	}
	commonDir, err := e.gitOutput(ctx, workDir, "rev-parse", "--git-common-dir")
	if err != nil {
		return repository{}, fmt.Errorf("resolve repository common directory: %w", err)
	}
	commonDir = strings.TrimSpace(commonDir)
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(workDir, commonDir)
	}
	canonicalRoot, err := canonicalPath(strings.TrimSpace(root))
	if err != nil {
		return repository{}, fmt.Errorf("canonicalize repository root: %w", err)
	}
	root, err = e.resolveRepositoryDisplayRoot(ctx, workDir, canonicalRoot, bare)
	if err != nil {
		return repository{}, err
	}
	commonDir, err = canonicalPath(commonDir)
	if err != nil {
		return repository{}, fmt.Errorf("canonicalize repository common directory: %w", err)
	}
	return repository{root: root, commonDir: commonDir}, nil
}

func (e *executorImpl) resolveRepositoryDisplayRoot(ctx context.Context, workDir, canonicalRoot string, bare bool) (string, error) {
	workDir, err := cleanAbsolutePath(workDir)
	if err != nil {
		return "", fmt.Errorf("resolve repository root spelling: %w", err)
	}
	candidate := workDir
	if !bare {
		prefix, prefixErr := e.gitOutput(ctx, workDir, "rev-parse", "--show-prefix")
		if prefixErr != nil {
			return "", fmt.Errorf("resolve repository path prefix: %w", prefixErr)
		}
		for part := range strings.SplitSeq(filepath.ToSlash(strings.TrimSpace(prefix)), "/") {
			if part != "" {
				candidate = filepath.Dir(candidate)
			}
		}
	}
	canonicalCandidate, err := canonicalPath(candidate)
	if err == nil && canonicalCandidate == canonicalRoot {
		return candidate, nil
	}
	return canonicalRoot, nil
}

func (e *executorImpl) lockRepository(ctx context.Context, commonDir string) error {
	// Every linked worktree shares the common Git directory, making this file a
	// stable cross-process lock for the complete inspect-and-mutate sequence.
	lock := flock.New(filepath.Join(commonDir, worktreeLockFile))
	locked, err := lock.TryLockContext(ctx, 25*time.Millisecond)
	if err != nil {
		_ = lock.Close()
		return fmt.Errorf("acquire repository lock: %w", err)
	}
	if !locked {
		_ = lock.Close()
		return fmt.Errorf("acquire repository lock: interrupted")
	}
	e.mu.Lock()
	e.repoLock = lock
	e.mu.Unlock()
	return nil
}

func (e *executorImpl) runWorktreeAdd(ctx context.Context, repo repository) error {
	cfg := e.worktreeCfg
	branch := cfg.Branch
	generated := !cfg.HasBranch
	if generated {
		digest := sha256.Sum256([]byte(e.dagRunID + "\x00" + e.stepIdentity))
		branch = "dagu/" + hex.EncodeToString(digest[:16])
	}
	if err := e.validateBranch(ctx, repo.root, branch); err != nil {
		return err
	}

	target := cfg.Path
	if !cfg.HasPath {
		target = filepath.Join(repo.root+".worktrees", filepath.FromSlash(branch))
	} else if !filepath.IsAbs(target) {
		target = filepath.Join(repo.root, target)
	}
	var err error
	target, err = cleanAbsolutePath(target)
	if err != nil {
		return fmt.Errorf("resolve worktree path %q: %w", cfg.Path, err)
	}
	targetKey, err := canonicalPath(target)
	if err != nil {
		return fmt.Errorf("resolve worktree path %q: %w", cfg.Path, err)
	}

	registrations, err := e.listWorktrees(ctx, repo.root)
	if err != nil {
		return err
	}
	byPath := registrationByPath(registrations, targetKey)
	byBranch := registrationByBranch(registrations, branch)
	if byPath != nil && byBranch != nil && byPath == byBranch && !byPath.primary && !byPath.bare {
		stale, staleErr := registrationStale(*byPath)
		if staleErr != nil {
			return staleErr
		}
		if stale {
			return fmt.Errorf("worktree %q has a stale registration; use git.worktree.remove to unregister it before retrying", target)
		}
		e.publishAddOutputs(target, branch, byPath.head, false, false)
		return nil
	}
	if byBranch != nil {
		return fmt.Errorf("branch %q is already checked out at %q", branch, byBranch.path)
	}
	if byPath != nil {
		return fmt.Errorf("path %q is already registered for branch %q", target, shortBranch(byPath.branch))
	}
	if err := ensureWorktreeTarget(target); err != nil {
		return err
	}

	branchRef := "refs/heads/" + branch
	commit, branchExists, err := e.resolveExactCommit(ctx, repo.root, branchRef)
	if err != nil {
		return err
	}
	branchCreated := !branchExists
	args := []string{"worktree", "add"}
	if branchExists {
		args = append(args, "--", target, branch)
	} else {
		if !generated && !cfg.CreateBranch {
			return fmt.Errorf("branch %q does not exist; set create_branch to create it", branch)
		}
		commit, err = e.resolveAddBase(ctx, repo.root, cfg)
		if err != nil {
			return err
		}
		args = append(args, "-b", branch, "--", target, commit)
	}
	if _, err := e.gitOutput(ctx, repo.root, args...); err != nil {
		return e.failedAddError(ctx, repo.root, target, branch, fmt.Errorf("create worktree %q: %w", target, err))
	}

	registrations, err = e.listWorktrees(ctx, repo.root)
	if err != nil {
		return err
	}
	created := registrationByPath(registrations, targetKey)
	if created == nil || created.branch != branchRef {
		return e.failedAddError(ctx, repo.root, target, branch,
			fmt.Errorf("created worktree registration does not match path %q and branch %q", target, branch))
	}
	stale, err := registrationStale(*created)
	if err != nil {
		return e.failedAddError(ctx, repo.root, target, branch, err)
	}
	if stale {
		return e.failedAddError(ctx, repo.root, target, branch, fmt.Errorf("created worktree %q is missing", target))
	}
	commit = created.head
	e.publishAddOutputs(target, branch, commit, true, branchCreated)
	return nil
}

func (e *executorImpl) publishAddOutputs(path, branch, commit string, worktreeCreated, branchCreated bool) {
	e.mu.Lock()
	e.outputs = map[string]any{
		"path":             path,
		"branch":           branch,
		"commit":           commit,
		"worktree_created": worktreeCreated,
		"branch_created":   branchCreated,
	}
	e.mu.Unlock()
}

func (e *executorImpl) resolveAddBase(ctx context.Context, repoRoot string, cfg worktreeConfig) (string, error) {
	if !cfg.HasBase {
		commit, ok, err := e.resolveExactCommit(ctx, repoRoot, "HEAD")
		if err != nil {
			return "", err
		}
		if !ok {
			return "", fmt.Errorf("repository HEAD does not resolve to a commit")
		}
		return commit, nil
	}
	base := cfg.Base
	candidates := make([]string, 0, 4)
	if commitHashPattern.MatchString(base) {
		candidates = append(candidates, base)
	}
	candidates = append(candidates,
		"refs/heads/"+base,
		"refs/remotes/origin/"+base,
		"refs/tags/"+base,
	)
	for _, candidate := range candidates {
		commit, ok, err := e.resolveExactCommit(ctx, repoRoot, candidate)
		if err != nil {
			return "", err
		}
		if ok {
			return commit, nil
		}
	}
	return "", fmt.Errorf("base %q does not resolve to a local commit", base)
}

func (e *executorImpl) resolveExactCommit(ctx context.Context, repoRoot, ref string) (string, bool, error) {
	output, err := e.gitRaw(ctx, repoRoot, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err == nil {
		return strings.TrimSpace(string(output)), true, nil
	}
	if exitCode := gitExitCode(err); exitCode == 128 || exitCode == 1 {
		return "", false, nil
	}
	return "", false, fmt.Errorf("resolve %q: %w", ref, gitCommandError(err, output))
}

func (e *executorImpl) validateBranch(ctx context.Context, repoRoot, branch string) error {
	output, err := e.gitRaw(ctx, repoRoot, "check-ref-format", "--branch", branch)
	if err != nil {
		return fmt.Errorf("invalid branch %q: %w", branch, gitCommandError(err, output))
	}
	return nil
}

func (e *executorImpl) failedAddError(ctx context.Context, repoRoot, target, branch string, operationErr error) error {
	if repairErr := e.repairFailedAdd(ctx, repoRoot, target, branch); repairErr != nil {
		return errors.Join(operationErr, fmt.Errorf("repair failed worktree add: %w", repairErr))
	}
	return operationErr
}

func (e *executorImpl) repairFailedAdd(ctx context.Context, repoRoot, target, branch string) error {
	// A canceled add still gets one bounded repair attempt. Removal is restricted
	// to the exact registration when its directory never became live.
	repairCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), failedAddRepairTimeout)
	defer cancel()

	registrations, err := e.listWorktrees(repairCtx, repoRoot)
	if err != nil {
		return err
	}
	targetKey, err := canonicalPath(target)
	if err != nil {
		return err
	}
	registration := registrationByPath(registrations, targetKey)
	if registration == nil || shortBranch(registration.branch) != branch {
		return nil
	}
	if _, err := os.Stat(target); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect failed worktree %q: %w", target, err)
	}
	output, err := e.gitRaw(repairCtx, repoRoot, "worktree", "remove", "--force", "--", target)
	if err != nil {
		return gitCommandError(err, output)
	}
	return nil
}

func ensureWorktreeTarget(path string) error {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect worktree path %q: %w", path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("worktree path %q exists and is not a directory", path)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("inspect worktree path %q: %w", path, err)
	}
	if len(entries) != 0 {
		return fmt.Errorf("worktree path %q is not empty", path)
	}
	return nil
}

func (e *executorImpl) runWorktreeRemove(ctx context.Context, repo repository) error {
	cfg := e.worktreeCfg
	registrations, err := e.listWorktrees(ctx, repo.root)
	if err != nil {
		return err
	}

	path := ""
	var byPath *worktreeRegistration
	if cfg.HasPath {
		path = cfg.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(repo.root, path)
		}
		path, err = cleanAbsolutePath(path)
		if err != nil {
			return fmt.Errorf("resolve worktree path %q: %w", cfg.Path, err)
		}
		pathKey, canonicalErr := canonicalPath(path)
		if canonicalErr != nil {
			return fmt.Errorf("resolve worktree path %q: %w", cfg.Path, canonicalErr)
		}
		if registration := registrationByPath(registrations, pathKey); registration != nil && registration.primary {
			return fmt.Errorf("refusing to remove primary working tree %q", path)
		}
		byPath = linkedRegistrationByPath(registrations, pathKey)
	}

	var byBranch *worktreeRegistration
	if cfg.HasBranch {
		if err := e.validateBranch(ctx, repo.root, cfg.Branch); err != nil {
			return err
		}
		byBranch = linkedRegistrationByBranch(registrations, cfg.Branch)
	}
	var target *worktreeRegistration
	if cfg.HasPath && cfg.HasBranch {
		if byPath != nil || byBranch != nil {
			if byPath == nil || byBranch == nil || byPath.path != byBranch.path {
				return fmt.Errorf("branch %q and path %q identify different worktrees", cfg.Branch, path)
			}
			target = byPath
		}
	} else if cfg.HasPath {
		target = byPath
	} else {
		target = byBranch
		if target != nil {
			path = repositoryPathSpelling(repo, target.path)
		}
	}

	branch := cfg.Branch
	if branch == "" && target != nil {
		branch = shortBranch(target.branch)
	}
	if target != nil && cfg.HasBranch && shortBranch(target.branch) != cfg.Branch {
		return fmt.Errorf("path %q is registered for branch %q, not %q", path, shortBranch(target.branch), cfg.Branch)
	}

	// Complete every safety check before mutating the worktree or branch.
	stale := false
	if target != nil {
		stale, err = registrationStale(*target)
		if err != nil {
			return err
		}
		if !stale {
			dirty, dirtyErr := e.worktreeDirty(ctx, target.path)
			if dirtyErr != nil {
				return dirtyErr
			}
			if dirty && !cfg.Force {
				return fmt.Errorf("worktree %q has uncommitted changes", target.path)
			}
		}
	}

	branchExists := false
	if cfg.DeleteBranch {
		_, branchExists, err = e.resolveExactCommit(ctx, repo.root, "refs/heads/"+cfg.Branch)
		if err != nil {
			return err
		}
		if branchExists {
			if conflict := checkedOutRegistration(registrations, cfg.Branch, target); conflict != nil {
				return fmt.Errorf("branch %q is checked out at %q", cfg.Branch, conflict.path)
			}
			if !cfg.ForceDeleteBranch {
				output, mergeErr := e.gitRaw(ctx, repo.root, "merge-base", "--is-ancestor", "refs/heads/"+cfg.Branch, "HEAD")
				if mergeErr != nil {
					if gitExitCode(mergeErr) == 1 {
						return fmt.Errorf("branch %q is not merged into repository HEAD", cfg.Branch)
					}
					return fmt.Errorf("check whether branch %q is merged: %w", cfg.Branch, gitCommandError(mergeErr, output))
				}
			}
		}
	}

	worktreeRemoved := false
	if target != nil {
		args := []string{"worktree", "remove"}
		if stale || cfg.Force {
			args = append(args, "--force")
		}
		args = append(args, "--", target.path)
		if _, err := e.gitOutput(ctx, repo.root, args...); err != nil {
			return fmt.Errorf("remove worktree %q: %w", target.path, err)
		}
		registrations, err = e.listWorktrees(ctx, repo.root)
		if err != nil {
			return err
		}
		if linkedRegistrationByPath(registrations, target.path) != nil {
			return fmt.Errorf("worktree %q remains registered after removal", target.path)
		}
		worktreeRemoved = true
	}

	branchDeleted := false
	if cfg.DeleteBranch && branchExists {
		if conflict := checkedOutRegistration(registrations, cfg.Branch, nil); conflict != nil {
			return fmt.Errorf("branch %q is checked out at %q", cfg.Branch, conflict.path)
		}
		flag := "-d"
		if cfg.ForceDeleteBranch {
			flag = "-D"
		}
		if _, err := e.gitOutput(ctx, repo.root, "branch", flag, "--", cfg.Branch); err != nil {
			return fmt.Errorf("delete branch %q: %w", cfg.Branch, err)
		}
		branchDeleted = true
	}

	e.mu.Lock()
	e.outputs = map[string]any{
		"path":             path,
		"branch":           branch,
		"worktree_removed": worktreeRemoved,
		"branch_deleted":   branchDeleted,
	}
	e.mu.Unlock()
	return nil
}

func (e *executorImpl) worktreeDirty(ctx context.Context, path string) (bool, error) {
	output, err := e.gitRaw(ctx, path, "status", "--porcelain", "-z", "--untracked-files=all")
	if err != nil {
		return false, fmt.Errorf("inspect worktree %q: %w", path, gitCommandError(err, output))
	}
	return len(output) != 0, nil
}

func checkedOutRegistration(registrations []worktreeRegistration, branch string, except *worktreeRegistration) *worktreeRegistration {
	ref := "refs/heads/" + branch
	for i := range registrations {
		registration := &registrations[i]
		if registration.branch == ref && (except == nil || registration.path != except.path) {
			return registration
		}
	}
	return nil
}

func (e *executorImpl) listWorktrees(ctx context.Context, repoRoot string) ([]worktreeRegistration, error) {
	output, err := e.gitRaw(ctx, repoRoot, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, fmt.Errorf("list worktrees: %w", gitCommandError(err, output))
	}
	var registrations []worktreeRegistration
	current := worktreeRegistration{}
	flush := func() error {
		if current.path == "" {
			return nil
		}
		path, canonicalErr := canonicalPath(current.path)
		if canonicalErr != nil {
			return canonicalErr
		}
		current.path = path
		registrations = append(registrations, current)
		current = worktreeRegistration{}
		return nil
	}
	for field := range bytes.SplitSeq(output, []byte{0}) {
		line := string(field)
		if line == "" {
			if err := flush(); err != nil {
				return nil, fmt.Errorf("parse worktree registration: %w", err)
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, "worktree "):
			if current.path != "" {
				if err := flush(); err != nil {
					return nil, fmt.Errorf("parse worktree registration: %w", err)
				}
			}
			current.path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "HEAD "):
			current.head = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch "):
			current.branch = strings.TrimPrefix(line, "branch ")
		case line == "bare":
			current.bare = true
		}
	}
	if err := flush(); err != nil {
		return nil, fmt.Errorf("parse worktree registration: %w", err)
	}
	if len(registrations) > 0 && !registrations[0].bare {
		registrations[0].primary = true
	}
	return registrations, nil
}

func registrationByPath(registrations []worktreeRegistration, path string) *worktreeRegistration {
	for i := range registrations {
		if registrations[i].path == path {
			return &registrations[i]
		}
	}
	return nil
}

func registrationByBranch(registrations []worktreeRegistration, branch string) *worktreeRegistration {
	ref := "refs/heads/" + branch
	for i := range registrations {
		if registrations[i].branch == ref {
			return &registrations[i]
		}
	}
	return nil
}

func linkedRegistrationByPath(registrations []worktreeRegistration, path string) *worktreeRegistration {
	registration := registrationByPath(registrations, path)
	if registration == nil || registration.primary || registration.bare {
		return nil
	}
	return registration
}

func linkedRegistrationByBranch(registrations []worktreeRegistration, branch string) *worktreeRegistration {
	registration := registrationByBranch(registrations, branch)
	if registration == nil || registration.primary || registration.bare {
		return nil
	}
	return registration
}

func registrationStale(registration worktreeRegistration) (bool, error) {
	_, err := os.Stat(registration.path)
	if err == nil {
		return false, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	return false, fmt.Errorf("inspect registered worktree %q: %w", registration.path, err)
}

func shortBranch(ref string) string {
	return strings.TrimPrefix(ref, "refs/heads/")
}

func canonicalPath(path string) (string, error) {
	abs, err := cleanAbsolutePath(path)
	if err != nil {
		return "", err
	}
	existing := abs
	var suffix []string
	// A requested worktree may not exist yet. Resolve symlinks on the longest
	// existing ancestor, then restore the unresolved suffix.
	for {
		_, err = os.Lstat(existing)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return "", fmt.Errorf("no existing ancestor for %q", abs)
		}
		suffix = append([]string{filepath.Base(existing)}, suffix...)
		existing = parent
	}
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", err
	}
	parts := append([]string{resolved}, suffix...)
	return filepath.Join(parts...), nil
}

func cleanAbsolutePath(path string) (string, error) {
	return filepath.Abs(path)
}

func repositoryPathSpelling(repo repository, canonical string) string {
	// Branch-only removal starts from a canonical registration path. Reconstruct
	// the equivalent path beneath the user-visible repository spelling for outputs.
	display := repo.root
	for {
		resolved, err := filepath.EvalSymlinks(display)
		if err == nil && resolved != display {
			relative, relativeErr := filepath.Rel(resolved, canonical)
			if relativeErr == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				return filepath.Clean(filepath.Join(display, relative))
			}
		}
		parent := filepath.Dir(display)
		if parent == display {
			break
		}
		display = parent
	}
	return canonical
}

func (e *executorImpl) gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	output, err := e.gitRaw(ctx, dir, args...)
	if err != nil {
		return "", gitCommandError(err, output)
	}
	return strings.TrimSpace(string(output)), nil
}

func (e *executorImpl) gitRaw(ctx context.Context, dir string, args ...string) ([]byte, error) {
	gitPath, err := osExec.LookPath("git")
	if err != nil {
		return nil, fmt.Errorf("find git executable: %w", err)
	}
	commandArgs := append([]string{"-C", dir}, args...)
	cmd := osExec.CommandContext(ctx, gitPath, commandArgs...) //nolint:gosec // Arguments are passed directly to Git without a shell.
	cmd.Env = sanitizedGitEnvironment()
	return cmd.CombinedOutput()
}

func sanitizedGitEnvironment() []string {
	// Inherited Git plumbing variables can redirect `git -C` to unrelated state.
	environment := os.Environ()
	filtered := environment[:0]
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		switch name {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE":
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func gitCommandError(err error, output []byte) error {
	message := strings.TrimSpace(string(output))
	if message == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, message)
}

func gitExitCode(err error) int {
	if exitErr, ok := errors.AsType[*osExec.ExitError](err); ok {
		return exitErr.ExitCode()
	}
	return -1
}

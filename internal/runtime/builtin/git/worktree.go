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

// repository keeps display paths separate from canonical coordination paths.
type repository struct {
	// root preserves the user-visible path spelling used for relative paths and outputs.
	root string
	// commonDir is canonical so every worktree of a repository uses the same lock.
	commonDir string
}

// worktreeRegistration is a canonicalized record from Git's porcelain output.
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
	// Preserve the step's path spelling when it identifies the same repository.
	// Canonical paths remain reserved for registration comparison and locking.
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
	branch, generated := e.selectAddBranch()
	if err := e.validateBranch(ctx, repo.root, branch); err != nil {
		return err
	}

	target := cfg.Path
	if !cfg.HasPath {
		target = filepath.Join(repo.root+".worktrees", filepath.FromSlash(branch))
	}
	target, targetKey, err := resolveWorktreePath(repo.root, target)
	if err != nil {
		return fmt.Errorf("resolve worktree path %q: %w", cfg.Path, err)
	}

	registrations, err := e.listWorktrees(ctx, repo.root)
	if err != nil {
		return err
	}
	existing, err := reusableAddRegistration(registrations, targetKey, target, branch)
	if err != nil {
		return err
	}
	if existing != nil {
		e.publishAddOutputs(target, branch, existing.head, false, false)
		return nil
	}
	if err := ensureWorktreeTarget(target); err != nil {
		return err
	}

	branchRef := "refs/heads/" + branch
	_, branchExists, err := e.resolveExactCommit(ctx, repo.root, branchRef)
	if err != nil {
		return err
	}
	args := []string{"worktree", "add"}
	if branchExists {
		args = append(args, "--", target, branch)
	} else {
		if !generated && !cfg.CreateBranch {
			return fmt.Errorf("branch %q does not exist; set create_branch to create it", branch)
		}
		commit, err := e.resolveAddBase(ctx, repo.root, cfg)
		if err != nil {
			return err
		}
		args = append(args, "-b", branch, "--", target, commit)
	}
	if _, err := e.gitOutput(ctx, repo.root, args...); err != nil {
		return e.failedAddError(ctx, repo.root, target, branch, fmt.Errorf("create worktree %q: %w", target, err))
	}

	// Git success is not sufficient: publish outputs only after the expected
	// branch and live registration are visible in the repository.
	registrations, err = e.listWorktrees(ctx, repo.root)
	if err != nil {
		return err
	}
	commit, err := addedWorktreeCommit(registrations, targetKey, target, branchRef, branch)
	if err != nil {
		return e.failedAddError(ctx, repo.root, target, branch, err)
	}
	e.publishAddOutputs(target, branch, commit, true, !branchExists)
	return nil
}

func (e *executorImpl) selectAddBranch() (branch string, generated bool) {
	if e.worktreeCfg.HasBranch {
		return e.worktreeCfg.Branch, false
	}
	// The NUL separator keeps distinct run/step identity pairs unambiguous.
	digest := sha256.Sum256([]byte(e.dagRunID + "\x00" + e.stepIdentity))
	return "dagu/" + hex.EncodeToString(digest[:16]), true
}

func reusableAddRegistration(registrations []worktreeRegistration, targetKey, target, branch string) (*worktreeRegistration, error) {
	byPath := registrationByPath(registrations, targetKey)
	byBranch := registrationByBranch(registrations, branch)
	if byPath != nil && byPath == byBranch && !byPath.primary && !byPath.bare {
		stale, err := registrationStale(*byPath)
		if err != nil {
			return nil, err
		}
		if stale {
			return nil, fmt.Errorf("worktree %q has a stale registration; use git.worktree.remove to unregister it before retrying", target)
		}
		return byPath, nil
	}
	if byBranch != nil {
		return nil, fmt.Errorf("branch %q is already checked out at %q", branch, byBranch.path)
	}
	if byPath != nil {
		return nil, fmt.Errorf("path %q is already registered for branch %q", target, shortBranch(byPath.branch))
	}
	return nil, nil
}

func addedWorktreeCommit(registrations []worktreeRegistration, targetKey, target, branchRef, branch string) (string, error) {
	created := registrationByPath(registrations, targetKey)
	if created == nil || created.branch != branchRef {
		return "", fmt.Errorf("created worktree registration does not match path %q and branch %q", target, branch)
	}
	stale, err := registrationStale(*created)
	if err != nil {
		return "", err
	}
	if stale {
		return "", fmt.Errorf("created worktree %q is missing", target)
	}
	return created.head, nil
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
	exitCode := gitExitCode(err)
	// An unresolved ref is an expected negative result for this probe.
	if exitCode == 128 || exitCode == 1 {
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

func resolveWorktreePath(repoRoot, path string) (string, string, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(repoRoot, path)
	}
	resolved, err := cleanAbsolutePath(path)
	if err != nil {
		return "", "", err
	}
	canonical, err := canonicalPath(resolved)
	if err != nil {
		return "", "", err
	}
	return resolved, canonical, nil
}

func (e *executorImpl) runWorktreeRemove(ctx context.Context, repo repository) error {
	registrations, err := e.listWorktrees(ctx, repo.root)
	if err != nil {
		return err
	}

	path, branch, target, err := e.resolveRemoveTarget(ctx, repo, registrations)
	if err != nil {
		return err
	}

	// Complete every safety check before the first mutation. In particular, a
	// rejected branch deletion must not remove the worktree first.
	stale, err := e.preflightWorktreeRemoval(ctx, target)
	if err != nil {
		return err
	}
	branchExists, err := e.preflightBranchDeletion(ctx, repo.root, registrations, target)
	if err != nil {
		return err
	}

	registrations, worktreeRemoved, err := e.removeRegisteredWorktree(ctx, repo.root, registrations, target, stale)
	if err != nil {
		return err
	}
	branchDeleted, err := e.deleteWorktreeBranch(ctx, repo.root, registrations, branchExists)
	if err != nil {
		return err
	}

	e.publishRemoveOutputs(path, branch, worktreeRemoved, branchDeleted)
	return nil
}

func (e *executorImpl) resolveRemoveTarget(
	ctx context.Context,
	repo repository,
	registrations []worktreeRegistration,
) (string, string, *worktreeRegistration, error) {
	cfg := e.worktreeCfg
	path := ""
	var byPath *worktreeRegistration
	if cfg.HasPath {
		var pathKey string
		var err error
		path, pathKey, err = resolveWorktreePath(repo.root, cfg.Path)
		if err != nil {
			return "", "", nil, fmt.Errorf("resolve worktree path %q: %w", cfg.Path, err)
		}
		for _, registration := range registrations {
			if registration.primary && registration.path == pathKey {
				return "", "", nil, fmt.Errorf("refusing to remove primary working tree %q", path)
			}
		}
		byPath = linkedRegistrationByPath(registrations, pathKey)
	}

	var byBranch *worktreeRegistration
	if cfg.HasBranch {
		if err := e.validateBranch(ctx, repo.root, cfg.Branch); err != nil {
			return "", "", nil, err
		}
		byBranch = linkedRegistrationByBranch(registrations, cfg.Branch)
	}

	var target *worktreeRegistration
	if cfg.HasPath && cfg.HasBranch {
		if byPath != nil || byBranch != nil {
			if byPath == nil || byBranch == nil || byPath.path != byBranch.path {
				return "", "", nil, fmt.Errorf("branch %q and path %q identify different worktrees", cfg.Branch, path)
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
		return "", "", nil, fmt.Errorf("path %q is registered for branch %q, not %q", path, shortBranch(target.branch), cfg.Branch)
	}
	return path, branch, target, nil
}

func (e *executorImpl) preflightWorktreeRemoval(ctx context.Context, target *worktreeRegistration) (bool, error) {
	if target == nil {
		return false, nil
	}
	stale, err := registrationStale(*target)
	if err != nil || stale {
		return stale, err
	}
	dirty, err := e.worktreeDirty(ctx, target.path)
	if err != nil {
		return false, err
	}
	if dirty && !e.worktreeCfg.Force {
		return false, fmt.Errorf("worktree %q has uncommitted changes", target.path)
	}
	return false, nil
}

func (e *executorImpl) preflightBranchDeletion(
	ctx context.Context,
	repoRoot string,
	registrations []worktreeRegistration,
	target *worktreeRegistration,
) (bool, error) {
	cfg := e.worktreeCfg
	if !cfg.DeleteBranch {
		return false, nil
	}
	_, branchExists, err := e.resolveExactCommit(ctx, repoRoot, "refs/heads/"+cfg.Branch)
	if err != nil || !branchExists {
		return branchExists, err
	}
	if conflict := checkedOutRegistration(registrations, cfg.Branch, target); conflict != nil {
		return false, fmt.Errorf("branch %q is checked out at %q", cfg.Branch, conflict.path)
	}
	if cfg.ForceDeleteBranch {
		return true, nil
	}
	output, err := e.gitRaw(ctx, repoRoot, "merge-base", "--is-ancestor", "refs/heads/"+cfg.Branch, "HEAD")
	if err == nil {
		return true, nil
	}
	if gitExitCode(err) == 1 {
		return false, fmt.Errorf("branch %q is not merged into repository HEAD", cfg.Branch)
	}
	return false, fmt.Errorf("check whether branch %q is merged: %w", cfg.Branch, gitCommandError(err, output))
}

func (e *executorImpl) removeRegisteredWorktree(
	ctx context.Context,
	repoRoot string,
	registrations []worktreeRegistration,
	target *worktreeRegistration,
	stale bool,
) ([]worktreeRegistration, bool, error) {
	if target == nil {
		return registrations, false, nil
	}
	args := []string{"worktree", "remove"}
	if stale || e.worktreeCfg.Force {
		args = append(args, "--force")
	}
	args = append(args, "--", target.path)
	if _, err := e.gitOutput(ctx, repoRoot, args...); err != nil {
		return nil, false, fmt.Errorf("remove worktree %q: %w", target.path, err)
	}
	registrations, err := e.listWorktrees(ctx, repoRoot)
	if err != nil {
		return nil, false, err
	}
	if linkedRegistrationByPath(registrations, target.path) != nil {
		return nil, false, fmt.Errorf("worktree %q remains registered after removal", target.path)
	}
	return registrations, true, nil
}

func (e *executorImpl) deleteWorktreeBranch(
	ctx context.Context,
	repoRoot string,
	registrations []worktreeRegistration,
	branchExists bool,
) (bool, error) {
	cfg := e.worktreeCfg
	if !cfg.DeleteBranch || !branchExists {
		return false, nil
	}
	// Recheck after worktree removal so a branch is never deleted while another
	// registration still reports it as checked out.
	if conflict := checkedOutRegistration(registrations, cfg.Branch, nil); conflict != nil {
		return false, fmt.Errorf("branch %q is checked out at %q", cfg.Branch, conflict.path)
	}
	flag := "-d"
	if cfg.ForceDeleteBranch {
		flag = "-D"
	}
	if _, err := e.gitOutput(ctx, repoRoot, "branch", flag, "--", cfg.Branch); err != nil {
		return false, fmt.Errorf("delete branch %q: %w", cfg.Branch, err)
	}
	return true, nil
}

func (e *executorImpl) publishRemoveOutputs(path, branch string, worktreeRemoved, branchDeleted bool) {
	e.mu.Lock()
	e.outputs = map[string]any{
		"path":             path,
		"branch":           branch,
		"worktree_removed": worktreeRemoved,
		"branch_deleted":   branchDeleted,
	}
	e.mu.Unlock()
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
		if registration.branch != ref {
			continue
		}
		if except != nil && registration.path == except.path {
			continue
		}
		return registration
	}
	return nil
}

func (e *executorImpl) listWorktrees(ctx context.Context, repoRoot string) ([]worktreeRegistration, error) {
	// NUL-delimited porcelain preserves paths containing whitespace or newlines.
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
	// Git lists the primary working tree first; a bare repository has no primary tree.
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
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
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
	blocked := map[string]bool{
		"GIT_DIR":        true,
		"GIT_WORK_TREE":  true,
		"GIT_COMMON_DIR": true,
		"GIT_INDEX_FILE": true,
	}
	environment := os.Environ()
	filtered := environment[:0]
	for _, entry := range environment {
		name, _, ok := strings.Cut(entry, "=")
		if ok && blocked[name] {
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
	var exitErr *osExec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

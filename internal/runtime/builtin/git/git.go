// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package git

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/runtime/executor"
	gogit "github.com/go-git/go-git/v5"
	gogitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
)

const (
	executorType = "git"
	remoteName   = "origin"

	opCheckout = "checkout"
)

var (
	_ executor.Executor  = (*executorImpl)(nil)
	_ executor.ExitCoder = (*executorImpl)(nil)
)

type executorImpl struct {
	mu       sync.Mutex
	stdout   io.Writer
	stderr   io.Writer
	cancel   context.CancelFunc
	kill     context.Context
	cfg      config
	op       string
	workDir  string
	exitCode int
}

type checkoutResult struct {
	Operation string `json:"operation"`
	Path      string `json:"path"`
	Ref       string `json:"ref,omitempty"`
	Commit    string `json:"commit"`
	Cloned    bool   `json:"cloned"`
	Changed   bool   `json:"changed"`
}

func init() {
	executor.RegisterExecutor(executorType, newExecutor, validateStep, core.ExecutorCapabilities{Command: true})
}

func newExecutor(ctx context.Context, step core.Step) (executor.Executor, error) {
	cfg := config{}
	if err := decodeConfig(step.ExecutorConfig.Config, &cfg); err != nil {
		return nil, err
	}
	op := stepOperation(step)
	if err := validateConfig(op, cfg); err != nil {
		return nil, err
	}

	kill, cancel := context.WithCancel(ctx)
	env := runtime.GetEnv(ctx)
	return &executorImpl{
		stdout:  os.Stdout,
		stderr:  os.Stderr,
		cancel:  cancel,
		kill:    kill,
		cfg:     cfg,
		op:      op,
		workDir: env.WorkingDir,
	}, nil
}

func validateStep(step core.Step) error {
	if step.ExecutorConfig.Type != executorType {
		return nil
	}
	cfg := config{}
	if err := decodeConfig(step.ExecutorConfig.Config, &cfg); err != nil {
		return err
	}
	return validateConfig(stepOperation(step), cfg)
}

func stepOperation(step core.Step) string {
	if len(step.Commands) == 0 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(step.Commands[0].Command))
}

func (e *executorImpl) SetStdout(out io.Writer) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stdout = out
}

func (e *executorImpl) SetStderr(out io.Writer) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stderr = out
}

func (e *executorImpl) Kill(_ os.Signal) error {
	e.mu.Lock()
	cancel := e.cancel
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

func (e *executorImpl) ExitCode() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.exitCode
}

func (e *executorImpl) Run(ctx context.Context) error {
	ctx, stop := e.runContext(ctx)
	defer stop()

	var err error
	switch e.op {
	case opCheckout:
		err = e.runCheckout(ctx)
	default:
		err = fmt.Errorf("git: unsupported operation %q", e.op)
	}

	e.mu.Lock()
	if err != nil {
		e.exitCode = 1
	} else {
		e.exitCode = 0
	}
	e.mu.Unlock()
	return err
}

func (e *executorImpl) runContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	go func() {
		select {
		case <-e.kill.Done():
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

func (e *executorImpl) runCheckout(ctx context.Context) error {
	target := e.resolvePath(e.cfg.Path)
	auth, err := e.auth()
	if err != nil {
		return err
	}

	cloned := false
	repo, err := gogit.PlainOpen(target)
	if err != nil {
		if !errors.Is(err, gogit.ErrRepositoryNotExists) {
			return fmt.Errorf("git checkout: open repository: %w", err)
		}
		if err := ensureCloneTarget(target); err != nil {
			return err
		}
		repo, err = e.clone(ctx, target, auth)
		if err != nil {
			return err
		}
		cloned = true
	}

	before := headHash(repo)
	if !cloned {
		if err := e.fetch(ctx, repo, auth); err != nil {
			return err
		}
	}
	commit, err := e.checkoutRef(ctx, repo, auth)
	if err != nil {
		return err
	}

	result := checkoutResult{
		Operation: opCheckout,
		Path:      target,
		Ref:       strings.TrimSpace(e.cfg.Ref),
		Commit:    commit,
		Cloned:    cloned,
		Changed:   cloned || before != commit,
	}
	return e.writeJSON(result)
}

func (e *executorImpl) clone(ctx context.Context, target string, auth transport.AuthMethod) (*gogit.Repository, error) {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, fmt.Errorf("git checkout: create parent directory: %w", err)
	}
	repo, err := gogit.PlainCloneContext(ctx, target, false, &gogit.CloneOptions{
		URL:   strings.TrimSpace(e.cfg.Repository),
		Auth:  auth,
		Depth: e.cfg.Depth,
	})
	if err != nil {
		return nil, fmt.Errorf("git checkout: clone failed: %w", err)
	}
	return repo, nil
}

func (e *executorImpl) fetch(ctx context.Context, repo *gogit.Repository, auth transport.AuthMethod) error {
	err := repo.FetchContext(ctx, &gogit.FetchOptions{
		Auth:       auth,
		RemoteName: remoteName,
		Depth:      e.cfg.Depth,
		Force:      true,
		RefSpecs: []gogitconfig.RefSpec{
			"+HEAD:refs/remotes/" + remoteName + "/HEAD",
			"+refs/heads/*:refs/remotes/" + remoteName + "/*",
			"+refs/tags/*:refs/tags/*",
		},
	})
	if err != nil && !errors.Is(err, gogit.NoErrAlreadyUpToDate) {
		return fmt.Errorf("git checkout: fetch failed: %w", err)
	}
	return nil
}

func (e *executorImpl) checkoutRef(ctx context.Context, repo *gogit.Repository, auth transport.AuthMethod) (string, error) {
	ref := strings.TrimSpace(e.cfg.Ref)
	if ref == "" {
		hash, name, ok := remoteDefaultHead(ctx, repo, auth)
		if ok {
			return e.checkoutHash(repo, hash, name)
		}
		return currentHead(repo)
	}

	hash, err := resolveRef(repo, ref)
	if err != nil {
		return "", err
	}
	return e.checkoutHash(repo, hash, ref)
}

func (e *executorImpl) checkoutHash(repo *gogit.Repository, hash plumbing.Hash, ref string) (string, error) {
	wt, err := repo.Worktree()
	if err != nil {
		return "", fmt.Errorf("git checkout: worktree: %w", err)
	}
	if err := wt.Checkout(&gogit.CheckoutOptions{Hash: hash, Force: e.cfg.Force}); err != nil {
		return "", fmt.Errorf("git checkout: checkout %q: %w", ref, err)
	}
	return hash.String(), nil
}

func remoteDefaultHead(ctx context.Context, repo *gogit.Repository, auth transport.AuthMethod) (plumbing.Hash, string, bool) {
	revisions := []plumbing.Revision{
		plumbing.Revision(plumbing.NewRemoteHEADReferenceName(remoteName)),
		plumbing.Revision(remoteName + "/HEAD"),
	}
	for _, revision := range revisions {
		hash, err := repo.ResolveRevision(revision)
		if err == nil && hash != nil {
			return *hash, string(revision), true
		}
	}

	remote, err := repo.Remote(remoteName)
	if err == nil {
		refs, err := remote.ListContext(ctx, &gogit.ListOptions{Auth: auth})
		if err == nil {
			for _, ref := range refs {
				if ref.Name() == plumbing.HEAD && !ref.Hash().IsZero() {
					return ref.Hash(), string(plumbing.HEAD), true
				}
			}
		}
	}
	return plumbing.ZeroHash, "", false
}

func resolveRef(repo *gogit.Repository, ref string) (plumbing.Hash, error) {
	if plumbing.IsHash(ref) {
		hash := plumbing.NewHash(ref)
		if _, err := repo.CommitObject(hash); err != nil {
			return plumbing.ZeroHash, fmt.Errorf("git checkout: resolve %q: %w", ref, err)
		}
		return hash, nil
	}

	revisions := []plumbing.Revision{
		plumbing.Revision(ref),
		plumbing.Revision("refs/heads/" + ref),
		plumbing.Revision("refs/remotes/origin/" + ref),
		plumbing.Revision("refs/tags/" + ref),
	}
	for _, revision := range revisions {
		hash, err := repo.ResolveRevision(revision)
		if err == nil && hash != nil {
			return *hash, nil
		}
	}
	return plumbing.ZeroHash, fmt.Errorf("git checkout: ref %q not found", ref)
}

func currentHead(repo *gogit.Repository) (string, error) {
	ref, err := repo.Head()
	if err != nil {
		return "", fmt.Errorf("git checkout: head: %w", err)
	}
	return ref.Hash().String(), nil
}

func headHash(repo *gogit.Repository) string {
	ref, err := repo.Head()
	if err != nil {
		return ""
	}
	return ref.Hash().String()
}

func ensureCloneTarget(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("git checkout: stat target: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("git checkout: target path exists and is not a directory")
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("git checkout: read target directory: %w", err)
	}
	if len(entries) > 0 {
		return fmt.Errorf("git checkout: target directory is not a git repository and is not empty")
	}
	return nil
}

func (e *executorImpl) auth() (transport.AuthMethod, error) {
	if e.cfg.SSHKeyPath != "" {
		auth, err := gitssh.NewPublicKeysFromFile("git", e.resolvePath(e.cfg.SSHKeyPath), e.cfg.SSHPassphrase)
		if err != nil {
			return nil, fmt.Errorf("git checkout: load ssh key: %w", err)
		}
		return auth, nil
	}
	if e.cfg.Token != "" {
		return &githttp.BasicAuth{Username: "git", Password: e.cfg.Token}, nil
	}
	if e.cfg.Password != "" {
		username := e.cfg.Username
		if username == "" {
			username = "git"
		}
		return &githttp.BasicAuth{Username: username, Password: e.cfg.Password}, nil
	}
	return nil, nil
}

func (e *executorImpl) resolvePath(path string) string {
	path = strings.TrimSpace(path)
	if filepath.IsAbs(path) || e.workDir == "" {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(e.workDir, path))
}

func (e *executorImpl) writeJSON(value any) error {
	e.mu.Lock()
	out := e.stdout
	e.mu.Unlock()
	return json.NewEncoder(out).Encode(value)
}

// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package git

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	gogit "github.com/go-git/go-git/v5"
	gogitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/gofrs/flock"
	cryptossh "golang.org/x/crypto/ssh"
)

const (
	executorType = "git"
	remoteName   = "origin"

	opCheckout       = "checkout"
	opWorktreeAdd    = "worktree.add"
	opWorktreeRemove = "worktree.remove"
)

var (
	_ executor.Executor                = (*executorImpl)(nil)
	_ executor.ExitCoder               = (*executorImpl)(nil)
	_ executor.OutputsProvider         = (*executorImpl)(nil)
	_ executor.DeclaredOutputsProvider = (*executorImpl)(nil)
	_ io.Closer                        = (*executorImpl)(nil)
)

type executorImpl struct {
	mu           sync.Mutex
	stdout       io.Writer
	stderr       io.Writer
	cancel       context.CancelFunc
	kill         context.Context
	cfg          config
	worktreeCfg  worktreeConfig
	op           string
	workDir      string
	dagRunID     string
	stepIdentity string
	outputs      map[string]any
	repoLock     *flock.Flock
	exitCode     int
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
	executor.RegisterExecutor(executorType, newExecutor, validateStep, registry.ExecutorCapabilities{Command: true})
}

func newExecutor(ctx context.Context, step ir.Step) (executor.Executor, error) {
	op := stepOperation(step)
	cfg := config{}
	worktreeCfg := worktreeConfig{}
	switch op {
	case opCheckout:
		if err := decodeConfig(step.ExecutorConfig.Config, &cfg); err != nil {
			return nil, err
		}
		if err := validateConfig(op, cfg); err != nil {
			return nil, err
		}
	case opWorktreeAdd, opWorktreeRemove:
		var err error
		worktreeCfg, err = decodeWorktreeConfig(op, step.ExecutorConfig.Config)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("git: unsupported operation %q", op)
	}

	kill, cancel := context.WithCancel(ctx)
	env := runtime.GetEnv(ctx)
	stepIdentity := step.ID
	if stepIdentity == "" {
		stepIdentity = step.Name
	}
	return &executorImpl{
		stdout:       os.Stdout,
		stderr:       os.Stderr,
		cancel:       cancel,
		kill:         kill,
		cfg:          cfg,
		worktreeCfg:  worktreeCfg,
		op:           op,
		workDir:      env.WorkingDir,
		dagRunID:     env.DAGRunID,
		stepIdentity: stepIdentity,
	}, nil
}

func validateStep(step ir.Step) error {
	if step.ExecutorConfig.Type != executorType {
		return nil
	}
	op := stepOperation(step)
	if op == opWorktreeAdd || op == opWorktreeRemove {
		_, err := decodeWorktreeConfig(op, step.ExecutorConfig.Config)
		return err
	}
	cfg := config{}
	if err := decodeConfig(step.ExecutorConfig.Config, &cfg); err != nil {
		return err
	}
	return validateConfig(op, cfg)
}

func stepOperation(step ir.Step) string {
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
	case opWorktreeAdd, opWorktreeRemove:
		err = e.runWorktree(ctx)
	default:
		err = fmt.Errorf("git: unsupported operation %q", e.op)
	}

	e.mu.Lock()
	if err != nil {
		e.exitCode = 1
		e.outputs = nil
	} else {
		e.exitCode = 0
	}
	e.mu.Unlock()
	if err != nil && (e.op == opWorktreeAdd || e.op == opWorktreeRemove) {
		e.mu.Lock()
		stderr := e.stderr
		e.mu.Unlock()
		_, _ = fmt.Fprintln(stderr, err)
	}
	return err
}

// GetOutputs returns the fixed output set published by a worktree action.
func (e *executorImpl) GetOutputs() map[string]any {
	e.mu.Lock()
	defer e.mu.Unlock()
	return maps.Clone(e.outputs)
}

// PublishesDeclaredOutputs exposes fixed action fields to strict output references.
func (e *executorImpl) PublishesDeclaredOutputs() bool {
	return e.op == opWorktreeAdd || e.op == opWorktreeRemove
}

// Close releases repository mutation serialization held for output capture.
func (e *executorImpl) Close() error {
	e.mu.Lock()
	lock := e.repoLock
	e.repoLock = nil
	e.mu.Unlock()
	if lock == nil {
		return nil
	}
	return lock.Close()
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
		auth, err := newPublicKeysFromFile("git", e.resolvePath(e.cfg.SSHKeyPath), e.cfg.SSHPassphrase)
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

// TODO: Delete this local key loader after go-git resolves the known_hosts regression.
// See https://github.com/go-git/go-git/issues/1551.
func newPublicKeysFromFile(user, keyPath, passphrase string) (*gitssh.PublicKeys, error) {
	// Leave host-key verification unset; go-git's transport owns known_hosts handling.
	key, err := os.ReadFile(keyPath) // #nosec G304 -- SSH key path is explicit git executor configuration.
	if err != nil {
		return nil, err
	}

	var signer cryptossh.Signer
	if passphrase == "" {
		signer, err = cryptossh.ParsePrivateKey(key)
	} else {
		signer, err = cryptossh.ParsePrivateKeyWithPassphrase(key, []byte(passphrase))
	}
	if err != nil {
		return nil, err
	}

	return &gitssh.PublicKeys{User: user, Signer: signer}, nil
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

// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package gitsync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/plumbing/transport/ssh"
)

// GitClient provides Git operations using go-git.
type GitClient struct {
	cfg      *Config
	repoPath string
	repo     *git.Repository
	mu       sync.Mutex
}

// shallowRepairDepth asks go-git to deepen a broken shallow checkout far
// enough to recover missing parent commits. Depth 0 does not repair a stale
// shallow boundary once the remote-tracking ref already points at the new tip.
const shallowRepairDepth = 2147483647

// NewGitClient creates a new Git client.
func NewGitClient(cfg *Config, repoPath string) *GitClient {
	return &GitClient{
		cfg:      cfg,
		repoPath: repoPath,
	}
}

// getAuth returns the appropriate authentication method based on config.
func (c *GitClient) getAuth() (transport.AuthMethod, error) {
	switch c.cfg.Auth.Type {
	case AuthTypeToken:
		if c.cfg.Auth.Token == "" {
			return nil, &ValidationError{Field: "auth.token", Message: "token is required for token auth"}
		}
		return &http.BasicAuth{
			Username: "git", // For GitHub, username can be anything
			Password: c.cfg.Auth.Token,
		}, nil

	case AuthTypeSSH:
		if c.cfg.Auth.SSHKeyPath == "" {
			return nil, &ValidationError{Field: "auth.sshKeyPath", Message: "SSH key path is required for SSH auth"}
		}
		auth, err := ssh.NewPublicKeysFromFile("git", c.cfg.Auth.SSHKeyPath, c.cfg.Auth.SSHPassphrase)
		if err != nil {
			return nil, fmt.Errorf("failed to load SSH key: %w", err)
		}
		return auth, nil

	default:
		// No auth
		return nil, nil
	}
}

// normalizeRepoURL normalizes the repository URL to a full clone URL.
func (c *GitClient) normalizeRepoURL() string {
	repo := c.cfg.Repository
	if repo == "" {
		return ""
	}
	if c.isFullURL(repo) {
		return repo
	}
	// Assume github.com/org/repo format and use HTTPS
	return "https://" + repo + ".git"
}

// isFullURL checks if the string is already a complete Git URL.
func (c *GitClient) isFullURL(s string) bool {
	return strings.HasPrefix(s, "https://") ||
		strings.HasPrefix(s, "http://") ||
		strings.HasPrefix(s, "git@") ||
		strings.HasPrefix(s, "ssh://")
}

// Clone clones the repository.
func (c *GitClient) Clone(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	auth, err := c.getAuth()
	if err != nil {
		return err
	}

	url := c.normalizeRepoURL()
	opts := &git.CloneOptions{
		URL:           url,
		Auth:          auth,
		ReferenceName: plumbing.NewBranchReferenceName(c.cfg.Branch),
		SingleBranch:  true,
		Depth:         1, // Shallow clone for performance
		Progress:      nil,
	}

	repo, err := git.PlainCloneContext(ctx, c.repoPath, false, opts)
	if err != nil {
		if err == transport.ErrAuthenticationRequired {
			return ErrAuthFailed
		}
		return &NetworkError{Operation: "clone", Cause: err}
	}

	c.repo = repo
	return nil
}

// Open opens an existing repository.
func (c *GitClient) Open() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	repo, err := git.PlainOpen(c.repoPath)
	if err != nil {
		if err == git.ErrRepositoryNotExists {
			return ErrRepoNotCloned
		}
		return fmt.Errorf("failed to open repository: %w", err)
	}
	c.repo = repo
	return nil
}

// IsCloned checks if the repository has been cloned.
func (c *GitClient) IsCloned() bool {
	_, err := os.Stat(filepath.Join(c.repoPath, ".git"))
	return err == nil
}

// Fetch fetches updates from the remote.
func (c *GitClient) Fetch(ctx context.Context) error {
	if err := c.requireRepo(); err != nil {
		return err
	}

	auth, err := c.getAuth()
	if err != nil {
		return err
	}

	err = c.repo.FetchContext(ctx, &git.FetchOptions{
		Auth:       auth,
		RemoteName: "origin",
		Depth:      1,
		Force:      true,
	})
	if err != nil && err != git.NoErrAlreadyUpToDate {
		return c.wrapAuthError(err, "fetch")
	}

	return nil
}

// Pull pulls updates and resets to the remote branch (hard reset for clean state).
func (c *GitClient) Pull(ctx context.Context) (*PullResult, error) {
	if err := c.requireRepo(); err != nil {
		return nil, err
	}

	wt, err := c.repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("failed to get worktree: %w", err)
	}

	auth, err := c.getAuth()
	if err != nil {
		return nil, err
	}

	// Get current HEAD
	headRef, err := c.repo.Head()
	if err != nil {
		return nil, fmt.Errorf("failed to get HEAD: %w", err)
	}

	result := &PullResult{
		PreviousCommit: headRef.Hash().String(),
		CurrentCommit:  headRef.Hash().String(),
	}

	err = c.pullWorktree(ctx, wt, auth)
	if err != nil {
		if err == git.NoErrAlreadyUpToDate {
			result.AlreadyUpToDate = true
			return result, nil
		}
		if errors.Is(err, plumbing.ErrObjectNotFound) && c.isShallow() {
			if repairErr := c.repairShallowHistory(ctx, auth); repairErr != nil {
				return nil, c.wrapAuthError(repairErr, "pull")
			}
			err = c.pullWorktree(ctx, wt, auth)
			if err == git.NoErrAlreadyUpToDate {
				result.AlreadyUpToDate = true
				return result, nil
			}
		}
		if err != nil {
			return nil, c.wrapAuthError(err, "pull")
		}
	}

	headRef, err = c.repo.Head()
	if err != nil {
		return nil, fmt.Errorf("failed to get HEAD: %w", err)
	}
	result.CurrentCommit = headRef.Hash().String()

	return result, nil
}

func (c *GitClient) pullWorktree(ctx context.Context, wt *git.Worktree, auth transport.AuthMethod) error {
	return wt.PullContext(ctx, &git.PullOptions{
		Auth:          auth,
		RemoteName:    "origin",
		ReferenceName: plumbing.NewBranchReferenceName(c.cfg.Branch),
		SingleBranch:  true,
		Force:         true,
	})
}

func (c *GitClient) isShallow() bool {
	shallow, err := c.repo.Storer.Shallow()
	return err == nil && len(shallow) > 0
}

func (c *GitClient) repairShallowHistory(ctx context.Context, auth transport.AuthMethod) error {
	err := c.repo.FetchContext(ctx, &git.FetchOptions{
		Auth:       auth,
		RemoteName: "origin",
		Depth:      shallowRepairDepth,
		Force:      true,
	})
	if err != nil && err != git.NoErrAlreadyUpToDate {
		return err
	}
	return nil
}

// PullResult represents the result of a pull operation.
type PullResult struct {
	PreviousCommit  string
	CurrentCommit   string
	AlreadyUpToDate bool
}

// GetHeadCommit returns the current HEAD commit hash.
func (c *GitClient) GetHeadCommit() (string, error) {
	if err := c.requireRepo(); err != nil {
		return "", err
	}

	ref, err := c.repo.Head()
	if err != nil {
		return "", fmt.Errorf("failed to get HEAD: %w", err)
	}

	return ref.Hash().String(), nil
}

// GetRemoteCommit returns the latest commit hash from the remote branch.
func (c *GitClient) GetRemoteCommit() (string, error) {
	if err := c.requireRepo(); err != nil {
		return "", err
	}

	ref, err := c.repo.Reference(plumbing.NewRemoteReferenceName("origin", c.cfg.Branch), true)
	if err != nil {
		return "", fmt.Errorf("failed to get remote reference: %w", err)
	}

	return ref.Hash().String(), nil
}

// GetCommitInfo returns information about a commit.
func (c *GitClient) GetCommitInfo(commitHash string) (*CommitInfo, error) {
	if err := c.requireRepo(); err != nil {
		return nil, err
	}

	commit, err := c.repo.CommitObject(plumbing.NewHash(commitHash))
	if err != nil {
		return nil, fmt.Errorf("failed to get commit: %w", err)
	}

	return &CommitInfo{
		Hash:      commitHash,
		Author:    commit.Author.Name,
		Email:     commit.Author.Email,
		Message:   commit.Message,
		Timestamp: commit.Author.When,
	}, nil
}

// CommitInfo represents information about a Git commit.
type CommitInfo struct {
	Hash      string
	Author    string
	Email     string
	Message   string
	Timestamp time.Time
}

// AddAndCommit stages a file and creates a commit.
// If the file content is already identical to HEAD (no changes), it returns
// the current HEAD hash instead of failing with an empty-commit error.
func (c *GitClient) AddAndCommit(filePath, message string) (string, error) {
	if err := c.requireRepo(); err != nil {
		return "", err
	}

	wt, err := c.repo.Worktree()
	if err != nil {
		return "", fmt.Errorf("failed to get worktree: %w", err)
	}

	if _, err = wt.Add(filePath); err != nil {
		return "", fmt.Errorf("failed to stage file: %w", err)
	}

	commit, err := wt.Commit(message, &git.CommitOptions{
		Author: &object.Signature{
			Name:  c.cfg.GetAuthorName(),
			Email: c.cfg.GetAuthorEmail(),
			When:  time.Now(),
		},
	})
	if err != nil {
		if errors.Is(err, git.ErrEmptyCommit) {
			// Content already matches HEAD — return HEAD hash so the
			// caller can proceed with push and state update.
			return c.GetHeadCommit()
		}
		return "", fmt.Errorf("failed to create commit: %w", err)
	}

	return commit.String(), nil
}

// RemoveFile stages a file removal (does not commit).
func (c *GitClient) RemoveFile(filePath string) error {
	if err := c.requireRepo(); err != nil {
		return err
	}

	wt, err := c.repo.Worktree()
	if err != nil {
		return fmt.Errorf("failed to get worktree: %w", err)
	}

	if _, err := wt.Remove(filePath); err != nil {
		return fmt.Errorf("failed to stage removal of %s: %w", filePath, err)
	}

	return nil
}

// RemoveFiles stages multiple file removals (does not commit).
func (c *GitClient) RemoveFiles(filePaths []string) error {
	if err := c.requireRepo(); err != nil {
		return err
	}

	wt, err := c.repo.Worktree()
	if err != nil {
		return fmt.Errorf("failed to get worktree: %w", err)
	}

	for _, filePath := range filePaths {
		if _, err := wt.Remove(filePath); err != nil {
			return fmt.Errorf("failed to stage removal of %s: %w", filePath, err)
		}
	}

	return nil
}

// CommitStaged creates a commit from the currently staged changes.
// If no changes are staged (clean tree), it returns the current HEAD hash.
func (c *GitClient) CommitStaged(message string) (string, error) {
	if err := c.requireRepo(); err != nil {
		return "", err
	}

	wt, err := c.repo.Worktree()
	if err != nil {
		return "", fmt.Errorf("failed to get worktree: %w", err)
	}

	commit, err := wt.Commit(message, &git.CommitOptions{
		Author: &object.Signature{
			Name:  c.cfg.GetAuthorName(),
			Email: c.cfg.GetAuthorEmail(),
			When:  time.Now(),
		},
	})
	if err != nil {
		if errors.Is(err, git.ErrEmptyCommit) {
			return c.GetHeadCommit()
		}
		return "", fmt.Errorf("failed to create commit: %w", err)
	}

	return commit.String(), nil
}

// Push pushes commits to the remote.
func (c *GitClient) Push(ctx context.Context) error {
	if err := c.requireRepo(); err != nil {
		return err
	}
	if !c.cfg.PushEnabled {
		return ErrPushDisabled
	}

	auth, err := c.getAuth()
	if err != nil {
		return err
	}

	err = c.repo.PushContext(ctx, &git.PushOptions{
		Auth:       auth,
		RemoteName: "origin",
	})
	if err == nil || err == git.NoErrAlreadyUpToDate {
		return nil
	}
	return c.wrapAuthError(err, "push")
}

// Reset resets a file to the version in HEAD.
func (c *GitClient) Reset(filePath string) error {
	if err := c.requireRepo(); err != nil {
		return err
	}

	ref, err := c.repo.Head()
	if err != nil {
		return fmt.Errorf("failed to get HEAD: %w", err)
	}

	commit, err := c.repo.CommitObject(ref.Hash())
	if err != nil {
		return fmt.Errorf("failed to get commit: %w", err)
	}

	tree, err := commit.Tree()
	if err != nil {
		return fmt.Errorf("failed to get tree: %w", err)
	}

	file, err := tree.File(filePath)
	if err != nil {
		return fmt.Errorf("failed to get file from tree: %w", err)
	}

	content, err := file.Contents()
	if err != nil {
		return fmt.Errorf("failed to read file content: %w", err)
	}

	fullPath := filepath.Join(c.repoPath, filePath)
	if err := os.WriteFile(fullPath, []byte(content), 0600); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

// FileExists checks if a file exists in the working tree.
func (c *GitClient) FileExists(filePath string) bool {
	fullPath := filepath.Join(c.repoPath, filePath)
	_, err := os.Stat(fullPath)
	return err == nil
}

// GetFilePath returns the full path to a file in the repository.
func (c *GitClient) GetFilePath(relativePath string) string {
	return filepath.Join(c.repoPath, relativePath)
}

// ListFiles returns all DAG files in the repository.
func (c *GitClient) ListFiles(extensions []string) ([]string, error) {
	var files []string
	basePath := c.repoPath
	if c.cfg.Path != "" {
		basePath = filepath.Join(c.repoPath, c.cfg.Path)
	}

	err := filepath.WalkDir(basePath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip .git directory
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}

		if d.IsDir() {
			return nil
		}

		// Check extension
		ext := filepath.Ext(path)
		for _, allowedExt := range extensions {
			if ext == allowedExt {
				relPath, err := filepath.Rel(c.repoPath, path)
				if err != nil {
					return err
				}
				files = append(files, relPath)
				break
			}
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to list files: %w", err)
	}

	return files, nil
}

// TestConnection tests the connection to the remote repository.
func (c *GitClient) TestConnection(_ context.Context) error {
	auth, err := c.getAuth()
	if err != nil {
		return err
	}

	remote := git.NewRemote(nil, &config.RemoteConfig{
		Name: "origin",
		URLs: []string{c.normalizeRepoURL()},
	})

	if _, err = remote.List(&git.ListOptions{Auth: auth}); err != nil {
		return c.wrapAuthError(err, "test connection")
	}

	return nil
}

// GetFileContentAtCommit returns the content of a file at a specific commit.
func (c *GitClient) GetFileContentAtCommit(filePath, commitHash string) ([]byte, error) {
	if err := c.requireRepo(); err != nil {
		return nil, err
	}

	commit, err := c.repo.CommitObject(plumbing.NewHash(commitHash))
	if err != nil {
		return nil, fmt.Errorf("failed to get commit: %w", err)
	}

	tree, err := commit.Tree()
	if err != nil {
		return nil, fmt.Errorf("failed to get tree: %w", err)
	}

	file, err := tree.File(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to get file: %w", err)
	}

	content, err := file.Contents()
	if err != nil {
		return nil, fmt.Errorf("failed to read file content: %w", err)
	}

	return []byte(content), nil
}

// SetupRemote ensures the remote is configured correctly.
func (c *GitClient) SetupRemote() error {
	if err := c.requireRepo(); err != nil {
		return err
	}

	url := c.normalizeRepoURL()

	remote, err := c.repo.Remote("origin")
	if err == git.ErrRemoteNotFound {
		_, err = c.repo.CreateRemote(&config.RemoteConfig{
			Name: "origin",
			URLs: []string{url},
		})
		if err != nil {
			return fmt.Errorf("failed to create remote: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get remote: %w", err)
	}

	// Check if URL matches
	cfg := remote.Config()
	if len(cfg.URLs) > 0 && cfg.URLs[0] != url {
		// Update remote URL
		err = c.repo.DeleteRemote("origin")
		if err != nil {
			return fmt.Errorf("failed to delete remote: %w", err)
		}
		_, err = c.repo.CreateRemote(&config.RemoteConfig{
			Name: "origin",
			URLs: []string{url},
		})
		if err != nil {
			return fmt.Errorf("failed to recreate remote: %w", err)
		}
	}

	return nil
}

// requireRepo returns an error if the repository is not initialized.
func (c *GitClient) requireRepo() error {
	if c.repo == nil {
		return ErrRepoNotCloned
	}
	return nil
}

// wrapAuthError converts authentication errors to the appropriate error type.
func (c *GitClient) wrapAuthError(err error, operation string) error {
	if err == transport.ErrAuthenticationRequired {
		return ErrAuthFailed
	}
	return &NetworkError{Operation: operation, Cause: err}
}

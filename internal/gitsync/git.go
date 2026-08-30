// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package gitsync

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/plumbing/transport/ssh"
	cryptossh "golang.org/x/crypto/ssh"
)

// GitClient provides Git operations using go-git.
type GitClient struct {
	cfg      *Config
	repoPath string
	repo     *git.Repository
	mu       sync.Mutex
}

// TrackedFile describes a regular file at HEAD.
type TrackedFile struct {
	Path       string
	Executable bool
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
		auth, err := newPublicKeysFromFile("git", c.cfg.Auth.SSHKeyPath, c.cfg.Auth.SSHPassphrase)
		if err != nil {
			return nil, fmt.Errorf("failed to load SSH key: %w", err)
		}
		return auth, nil

	default:
		// No auth
		return nil, nil
	}
}

// TODO: Delete this local key loader after go-git resolves the known_hosts regression.
// See https://github.com/go-git/go-git/issues/1551.
func newPublicKeysFromFile(user, keyPath, passphrase string) (*ssh.PublicKeys, error) {
	// Leave host-key verification unset; go-git's transport owns known_hosts handling.
	key, err := os.ReadFile(keyPath) // #nosec G304 -- SSH key path is explicit Git Sync configuration.
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

	return &ssh.PublicKeys{User: user, Signer: signer}, nil
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
	clean, err := c.cleanWorktree(wt)
	if err != nil {
		return err
	}
	if !clean {
		return git.ErrUnstagedChanges
	}

	err = wt.PullContext(ctx, &git.PullOptions{
		Auth:          auth,
		RemoteName:    "origin",
		ReferenceName: plumbing.NewBranchReferenceName(c.cfg.Branch),
		SingleBranch:  true,
		Force:         true,
	})
	if runtime.GOOS != "windows" || !errors.Is(err, git.ErrUnstagedChanges) {
		return err
	}

	// The worktree was verified before go-git's mode-sensitive reset.
	head, headErr := c.repo.Head()
	if headErr != nil {
		return headErr
	}
	return wt.Reset(&git.ResetOptions{Mode: git.HardReset, Commit: head.Hash()})
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

func (c *GitClient) addFile(filePath string) error {
	if err := c.requireRepo(); err != nil {
		return err
	}

	worktree, err := c.repo.Worktree()
	if err != nil {
		return fmt.Errorf("failed to get worktree: %w", err)
	}
	if _, err := worktree.Add(filePath); err != nil {
		return fmt.Errorf("failed to stage file: %w", err)
	}
	return nil
}

func (c *GitClient) addFileMode(filePath string, executable bool) error {
	if err := c.addFile(filePath); err != nil {
		return err
	}

	idx, err := c.repo.Storer.Index()
	if err != nil {
		return fmt.Errorf("failed to read Git index: %w", err)
	}
	entry, err := idx.Entry(filePath)
	if err != nil {
		return fmt.Errorf("failed to find staged file: %w", err)
	}
	entry.Mode = filemode.Regular
	if executable {
		entry.Mode = filemode.Executable
	}
	if err := c.repo.Storer.SetIndex(idx); err != nil {
		return fmt.Errorf("failed to update Git index: %w", err)
	}

	return nil
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

func (c *GitClient) commitAndPush(ctx context.Context, message string, stage func() error) (string, error) {
	if err := c.requireRepo(); err != nil {
		return "", err
	}

	worktree, err := c.repo.Worktree()
	if err != nil {
		return "", fmt.Errorf("failed to get worktree: %w", err)
	}
	clean, err := c.cleanWorktree(worktree)
	if err != nil {
		return "", fmt.Errorf("failed to inspect worktree: %w", err)
	}
	if !clean {
		return "", errors.New("cannot mutate dirty Git sync clone; pull to reconcile")
	}

	head, err := c.repo.Head()
	if err != nil {
		return "", fmt.Errorf("failed to get HEAD: %w", err)
	}
	rollback := func(operationErr error) error {
		if resetErr := worktree.Reset(&git.ResetOptions{Mode: git.HardReset, Commit: head.Hash()}); resetErr != nil {
			return errors.Join(operationErr, fmt.Errorf("failed to roll back Git mutation: %w", resetErr))
		}
		return operationErr
	}

	if err := stage(); err != nil {
		return "", rollback(err)
	}
	commitHash, err := c.CommitStaged(message)
	if err != nil {
		return "", rollback(err)
	}
	if err := c.Push(ctx); err != nil {
		return "", rollback(err)
	}

	return commitHash, nil
}

func (c *GitClient) cleanWorktree(worktree *git.Worktree) (bool, error) {
	status, err := worktree.Status()
	if err != nil {
		return false, err
	}
	if runtime.GOOS != "windows" || status.IsClean() {
		return status.IsClean(), nil
	}

	idx, err := c.repo.Storer.Index()
	if err != nil {
		return false, err
	}
	// Windows reports tracked executable files as mode-only modifications.
	for filePath, fileStatus := range status {
		if fileStatus.Staging == git.Unmodified && fileStatus.Worktree == git.Unmodified {
			continue
		}
		if fileStatus.Staging != git.Unmodified || fileStatus.Worktree != git.Modified {
			return false, nil
		}

		entry, err := idx.Entry(filePath)
		if err != nil || entry.Mode != filemode.Executable {
			return false, nil
		}
		info, err := worktree.Filesystem.Lstat(filePath)
		if err != nil {
			return false, err
		}
		if !info.Mode().IsRegular() {
			return false, nil
		}

		file, err := worktree.Filesystem.Open(filePath)
		if err != nil {
			return false, err
		}
		hasher := plumbing.NewHasher(plumbing.BlobObject, info.Size())
		_, copyErr := io.Copy(&hasher, file)
		closeErr := file.Close()
		if copyErr != nil {
			return false, copyErr
		}
		if closeErr != nil {
			return false, closeErr
		}
		if hasher.Sum() != entry.Hash {
			return false, nil
		}
	}

	return true, nil
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

// ListTrackedFiles returns regular files at HEAD below the configured path.
func (c *GitClient) ListTrackedFiles() ([]TrackedFile, error) {
	if err := c.requireRepo(); err != nil {
		return nil, err
	}

	head, err := c.repo.Head()
	if err != nil {
		return nil, fmt.Errorf("failed to get HEAD: %w", err)
	}
	commit, err := c.repo.CommitObject(head.Hash())
	if err != nil {
		return nil, fmt.Errorf("failed to get commit: %w", err)
	}
	tree, err := commit.Tree()
	if err != nil {
		return nil, fmt.Errorf("failed to get tree: %w", err)
	}

	if filepath.IsAbs(c.cfg.Path) || filepath.VolumeName(c.cfg.Path) != "" {
		return nil, &ValidationError{Field: "path", Message: "must stay within the repository"}
	}

	root := path.Clean(filepath.ToSlash(c.cfg.Path))
	if root == "." {
		root = ""
	}
	if path.IsAbs(root) || root == ".." || strings.HasPrefix(root, "../") {
		return nil, &ValidationError{Field: "path", Message: "must stay within the repository"}
	}
	prefix := strings.TrimSuffix(root, "/")
	if prefix != "" {
		prefix += "/"
	}

	var files []TrackedFile
	err = tree.Files().ForEach(func(file *object.File) error {
		if prefix != "" && !strings.HasPrefix(file.Name, prefix) {
			return nil
		}
		switch file.Mode {
		case filemode.Regular, filemode.Deprecated, filemode.Executable:
			if strings.Contains(file.Name, `\`) {
				return &ValidationError{
					Field:   "path",
					Message: fmt.Sprintf("tracked path %q contains an unsupported backslash", file.Name),
				}
			}
			files = append(files, TrackedFile{
				Path:       file.Name,
				Executable: file.Mode == filemode.Executable,
			})
		case filemode.Empty, filemode.Dir, filemode.Symlink, filemode.Submodule:
			return nil
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list tracked files: %w", err)
	}
	return files, nil
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

// ListFilesUnder returns repo-relative paths of all regular files below the
// given repo-relative subtree, regardless of extension. A missing subtree
// yields an empty list.
func (c *GitClient) ListFilesUnder(subDir string) ([]string, error) {
	basePath := c.repoPath
	if c.cfg.Path != "" {
		basePath = filepath.Join(c.repoPath, c.cfg.Path)
	}
	root := filepath.Join(basePath, filepath.FromSlash(subDir))

	var files []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		relPath, err := filepath.Rel(c.repoPath, path)
		if err != nil {
			return err
		}
		files = append(files, relPath)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list files under %s: %w", subDir, err)
	}
	return files, nil
}

// GetFileSizeAtCommit returns the blob size of a file at a specific commit
// without decoding its content.
func (c *GitClient) GetFileSizeAtCommit(filePath, commitHash string) (int64, error) {
	file, err := c.fileAtCommit(filePath, commitHash)
	if err != nil {
		return 0, err
	}
	return file.Size, nil
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
	file, err := c.fileAtCommit(filePath, commitHash)
	if err != nil {
		return nil, err
	}

	content, err := file.Contents()
	if err != nil {
		return nil, fmt.Errorf("failed to read file content: %w", err)
	}

	return []byte(content), nil
}

func (c *GitClient) inspectFileAtCommit(filePath, commitHash string, detectBinary bool) (int64, bool, error) {
	file, err := c.fileAtCommit(filePath, commitHash)
	if err != nil {
		return 0, false, err
	}
	if !detectBinary {
		return file.Size, false, nil
	}
	reader, err := file.Reader()
	if err != nil {
		return 0, false, fmt.Errorf("failed to read file content: %w", err)
	}
	defer func() {
		_ = reader.Close()
	}()
	binary, err := isBinaryReader(reader)
	if err != nil {
		return 0, false, fmt.Errorf("failed to inspect file content: %w", err)
	}
	return file.Size, binary, nil
}

func (c *GitClient) fileAtCommit(filePath, commitHash string) (*object.File, error) {
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
	return file, nil
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

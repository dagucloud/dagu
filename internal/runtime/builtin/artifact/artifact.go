// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package artifact

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/runtime/executor"
)

const (
	executorType = "artifact"

	opWrite = "write"
	opRead  = "read"
	opList  = "list"
)

var (
	errConfig      = errors.New("artifact: configuration error")
	errUnsupported = errors.New("artifact: unsupported operation")
)

var (
	_ executor.Executor  = (*executorImpl)(nil)
	_ executor.ExitCoder = (*executorImpl)(nil)
)

type executorImpl struct {
	mu          sync.Mutex
	stdout      io.Writer
	stderr      io.Writer
	cfg         config
	op          string
	artifactDir string
	exitCode    int
}

type artifactPath struct {
	rel string
	abs string
}

type pathInfo struct {
	Path      string    `json:"path"`
	Type      string    `json:"type"`
	Size      int64     `json:"size"`
	Mode      string    `json:"mode"`
	ModTime   time.Time `json:"modTime"`
	IsDir     bool      `json:"isDir"`
	IsRegular bool      `json:"isRegular"`
	IsSymlink bool      `json:"isSymlink"`
}

type opResult struct {
	Operation string     `json:"operation"`
	Path      string     `json:"path,omitempty"`
	Exists    *bool      `json:"exists,omitempty"`
	Type      string     `json:"type,omitempty"`
	Size      int64      `json:"size,omitempty"`
	Mode      string     `json:"mode,omitempty"`
	ModTime   *time.Time `json:"modTime,omitempty"`
	Files     int64      `json:"files,omitempty"`
	Bytes     int64      `json:"bytes,omitempty"`
	Created   *bool      `json:"created,omitempty"`
	Content   string     `json:"content,omitempty"`
	Entries   []pathInfo `json:"entries,omitempty"`
}

func init() {
	executor.RegisterExecutor(executorType, newExecutor, validateStep, core.ExecutorCapabilities{Command: true})
}

func newExecutor(ctx context.Context, step core.Step) (executor.Executor, error) {
	cfg := defaultConfig()
	if err := decodeConfig(step.ExecutorConfig.Config, &cfg); err != nil {
		return nil, err
	}

	op := stepOperation(step)
	if err := validateConfig(op, cfg); err != nil {
		return nil, err
	}

	artifactDir, err := artifactDirFromContext(ctx)
	if err != nil {
		return nil, err
	}

	return &executorImpl{
		stdout:      os.Stdout,
		stderr:      os.Stderr,
		cfg:         cfg,
		op:          op,
		artifactDir: artifactDir,
	}, nil
}

func validateStep(step core.Step) error {
	if step.ExecutorConfig.Type != executorType {
		return nil
	}
	cfg := defaultConfig()
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

func artifactDirFromContext(ctx context.Context) (string, error) {
	env := runtime.GetEnv(ctx)
	if env.Scope == nil {
		return "", fmt.Errorf("%w: %s is not set; enable artifacts for this DAG", errConfig, exec.EnvKeyDAGRunArtifactsDir)
	}
	artifactDir, ok := env.Scope.Get(exec.EnvKeyDAGRunArtifactsDir)
	if !ok || strings.TrimSpace(artifactDir) == "" {
		return "", fmt.Errorf("%w: %s is not set; enable artifacts for this DAG", errConfig, exec.EnvKeyDAGRunArtifactsDir)
	}
	return artifactDir, nil
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

func (*executorImpl) Kill(_ os.Signal) error { return nil }

func (e *executorImpl) ExitCode() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.exitCode
}

func (e *executorImpl) Run(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		e.setExitCode(1)
		return err
	}

	var err error
	switch e.op {
	case opWrite:
		err = e.runWrite()
	case opRead:
		err = e.runRead()
	case opList:
		err = e.runList()
	default:
		err = fmt.Errorf("%w: %q", errUnsupported, e.op)
	}

	if err != nil {
		e.setExitCode(1)
		return err
	}
	e.setExitCode(0)
	return nil
}

func (e *executorImpl) setExitCode(code int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.exitCode = code
}

func (e *executorImpl) runWrite() error {
	target, err := e.resolveWritablePath(e.cfg.Path)
	if err != nil {
		return err
	}

	mode := os.FileMode(0o600)
	if e.cfg.Mode != "" {
		mode, err = parseFileMode(e.cfg.Mode)
		if err != nil {
			return err
		}
	}

	existed, err := pathExists(target.abs)
	if err != nil {
		return err
	}
	if err := e.ensureWritableTargetInsideRoot(target.abs); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target.abs), 0o750); err != nil {
		return fmt.Errorf("artifact write %s: create parent directories: %w", target.rel, err)
	}
	if err := e.ensureWritableTargetInsideRoot(target.abs); err != nil {
		return err
	}

	data := []byte(e.cfg.Content)
	if !e.cfg.Overwrite {
		handle, err := os.OpenFile(target.abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode) //nolint:gosec // artifact path is constrained to the DAG-run artifact directory.
		if err != nil {
			return fmt.Errorf("artifact write %s: %w", target.rel, err)
		}
		if _, err := handle.Write(data); err != nil {
			_ = handle.Close()
			return fmt.Errorf("artifact write %s: %w", target.rel, err)
		}
		if err := handle.Close(); err != nil {
			return fmt.Errorf("artifact write %s: %w", target.rel, err)
		}
		created := true
		return e.writeJSON(opResult{Operation: opWrite, Path: target.rel, Bytes: int64(len(data)), Created: &created})
	}

	if err := fileutil.WriteFileAtomic(target.abs, data, mode); err != nil {
		return fmt.Errorf("artifact write %s: %w", target.rel, err)
	}

	created := !existed
	return e.writeJSON(opResult{Operation: opWrite, Path: target.rel, Bytes: int64(len(data)), Created: &created})
}

func (e *executorImpl) runRead() error {
	target, err := e.resolveExistingPath(e.cfg.Path, false)
	if err != nil {
		return err
	}
	info, err := os.Stat(target.abs)
	if err != nil {
		return fmt.Errorf("artifact read %s: %w", target.rel, err)
	}
	if info.IsDir() {
		return fmt.Errorf("artifact read %s: cannot read a directory", target.rel)
	}
	if e.cfg.MaxBytes > 0 && info.Size() > e.cfg.MaxBytes {
		return fmt.Errorf("artifact read %s: file size %d exceeds max_bytes %d", target.rel, info.Size(), e.cfg.MaxBytes)
	}

	if e.cfg.Format == "json" {
		data, err := os.ReadFile(target.abs) //nolint:gosec // artifact path is constrained to the DAG-run artifact directory.
		if err != nil {
			return fmt.Errorf("artifact read %s: %w", target.rel, err)
		}
		result := infoResult(opRead, target.rel, info)
		result.Content = string(data)
		result.Bytes = int64(len(data))
		return e.writeJSON(result)
	}

	handle, err := os.Open(target.abs) //nolint:gosec // artifact path is constrained to the DAG-run artifact directory.
	if err != nil {
		return fmt.Errorf("artifact read %s: %w", target.rel, err)
	}
	defer func() { _ = handle.Close() }()
	_, err = io.Copy(e.stdout, handle)
	return err
}

func (e *executorImpl) runList() error {
	target, err := e.resolveExistingPath(e.cfg.Path, true)
	if err != nil {
		return err
	}
	info, err := os.Stat(target.abs)
	if err != nil {
		return fmt.Errorf("artifact list %s: %w", target.rel, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("artifact list %s: path must be a directory", target.rel)
	}

	root, err := e.prepareRoot()
	if err != nil {
		return err
	}
	entries, err := e.listEntries(root, target.abs)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Path < entries[j].Path
	})
	return e.writeJSON(opResult{Operation: opList, Path: target.rel, Entries: entries, Files: countFiles(entries)})
}

func (e *executorImpl) listEntries(root, current string) ([]pathInfo, error) {
	var entries []pathInfo
	addEntry := func(path string, info fs.FileInfo) error {
		if samePath(path, current) {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if info.IsDir() && !e.cfg.IncludeDirs {
			return nil
		}
		if !matchesPattern(e.cfg.Pattern, rel) {
			return nil
		}
		entries = append(entries, newPathInfo(rel, info))
		return nil
	}

	if e.cfg.Recursive {
		err := filepath.WalkDir(current, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			return addEntry(path, info)
		})
		return entries, err
	}

	children, err := os.ReadDir(current)
	if err != nil {
		return nil, err
	}
	for _, child := range children {
		info, err := child.Info()
		if err != nil {
			return nil, err
		}
		if err := addEntry(filepath.Join(current, child.Name()), info); err != nil {
			return nil, err
		}
	}
	return entries, nil
}

func (e *executorImpl) resolveWritablePath(raw string) (artifactPath, error) {
	rel, err := cleanArtifactPath(raw, false)
	if err != nil {
		return artifactPath{}, err
	}
	root, err := e.prepareRoot()
	if err != nil {
		return artifactPath{}, err
	}
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := e.ensurePathInsideRoot(root, abs); err != nil {
		return artifactPath{}, err
	}
	return artifactPath{rel: rel, abs: abs}, nil
}

func (e *executorImpl) resolveExistingPath(raw string, allowRoot bool) (artifactPath, error) {
	rel, err := cleanArtifactPath(raw, allowRoot)
	if err != nil {
		return artifactPath{}, err
	}
	root, err := e.prepareRoot()
	if err != nil {
		return artifactPath{}, err
	}
	abs := root
	if rel != "." {
		abs = filepath.Join(root, filepath.FromSlash(rel))
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return artifactPath{}, err
	}
	resolved = filepath.Clean(resolved)
	if !pathInsideOrSame(root, resolved) {
		return artifactPath{}, fmt.Errorf("%w: path %q escapes artifact directory", errConfig, rel)
	}
	return artifactPath{rel: rel, abs: resolved}, nil
}

func (e *executorImpl) prepareRoot() (string, error) {
	rootRaw := strings.TrimSpace(e.artifactDir)
	if rootRaw == "" {
		return "", fmt.Errorf("%w: %s is not set", errConfig, exec.EnvKeyDAGRunArtifactsDir)
	}
	root, err := filepath.Abs(rootRaw)
	if err != nil {
		return "", fmt.Errorf("artifact: resolve artifact directory: %w", err)
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return "", fmt.Errorf("artifact: create artifact directory: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("artifact: resolve artifact directory: %w", err)
	}
	return filepath.Clean(resolved), nil
}

func cleanArtifactPath(raw string, allowRoot bool) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if allowRoot {
			return ".", nil
		}
		return "", fmt.Errorf("%w: path must not be empty", errConfig)
	}
	normalized := strings.ReplaceAll(raw, "\\", "/")
	if strings.HasPrefix(normalized, "/") ||
		strings.HasPrefix(normalized, "~/") ||
		normalized == "~" ||
		filepath.IsAbs(raw) ||
		hasWindowsDrive(raw) ||
		hasWindowsDrive(normalized) {
		return "", fmt.Errorf("%w: artifact path must be relative", errConfig)
	}
	if slices.Contains(strings.Split(normalized, "/"), "..") {
		return "", fmt.Errorf("%w: artifact path must not contain ..", errConfig)
	}

	clean := path.Clean(normalized)
	if clean == "." {
		if allowRoot {
			return ".", nil
		}
		return "", fmt.Errorf("%w: artifact path must name a file", errConfig)
	}
	if strings.HasPrefix(clean, "/") {
		return "", fmt.Errorf("%w: artifact path must be relative", errConfig)
	}
	return clean, nil
}

func (e *executorImpl) ensureWritableTargetInsideRoot(target string) error {
	root, err := e.prepareRoot()
	if err != nil {
		return err
	}
	if err := e.ensurePathInsideRoot(root, filepath.Dir(target)); err != nil {
		return err
	}
	if exists, err := pathExists(target); err != nil {
		return err
	} else if exists {
		resolved, err := filepath.EvalSymlinks(target)
		if err != nil {
			return err
		}
		if !pathInsideOrSame(root, resolved) {
			return fmt.Errorf("%w: path %q escapes artifact directory", errConfig, target)
		}
	}
	return nil
}

func (e *executorImpl) ensurePathInsideRoot(root, target string) error {
	existing, err := nearestExistingPath(target)
	if err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return err
	}
	if !pathInsideOrSame(root, resolved) {
		return fmt.Errorf("%w: path %q escapes artifact directory", errConfig, target)
	}
	return nil
}

func nearestExistingPath(target string) (string, error) {
	current := filepath.Clean(target)
	for {
		if _, err := os.Lstat(current); err == nil {
			return current, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		next := filepath.Dir(current)
		if next == current {
			return "", fmt.Errorf("%w: no existing parent for path %q", errConfig, target)
		}
		current = next
	}
}

func pathExists(path string) (bool, error) {
	if _, err := os.Lstat(path); err == nil {
		return true, nil
	} else if os.IsNotExist(err) {
		return false, nil
	} else {
		return false, err
	}
}

func pathInsideOrSame(parent, child string) bool {
	parent = filepath.Clean(parent)
	child = filepath.Clean(child)
	if samePath(parent, child) {
		return true
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func samePath(a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if goruntime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func hasWindowsDrive(value string) bool {
	if len(value) < 2 || value[1] != ':' {
		return false
	}
	ch := value[0]
	return (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z')
}

func infoResult(operation, rel string, info fs.FileInfo) opResult {
	modTime := info.ModTime()
	exists := true
	return opResult{
		Operation: operation,
		Path:      rel,
		Exists:    &exists,
		Type:      fileType(info),
		Size:      info.Size(),
		Mode:      info.Mode().String(),
		ModTime:   &modTime,
	}
}

func newPathInfo(rel string, info fs.FileInfo) pathInfo {
	return pathInfo{
		Path:      rel,
		Type:      fileType(info),
		Size:      info.Size(),
		Mode:      info.Mode().String(),
		ModTime:   info.ModTime(),
		IsDir:     info.IsDir(),
		IsRegular: info.Mode().IsRegular(),
		IsSymlink: info.Mode()&os.ModeSymlink != 0,
	}
}

func fileType(info fs.FileInfo) string {
	mode := info.Mode()
	switch {
	case mode&os.ModeSymlink != 0:
		return "symlink"
	case info.IsDir():
		return "directory"
	case mode.IsRegular():
		return "file"
	default:
		return "other"
	}
}

func matchesPattern(pattern, value string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return true
	}
	ok, err := doublestar.Match(pattern, filepath.ToSlash(value))
	return err == nil && ok
}

func countFiles(entries []pathInfo) int64 {
	var count int64
	for _, entry := range entries {
		if entry.Type == "file" {
			count++
		}
	}
	return count
}

func (e *executorImpl) writeJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = e.stdout.Write(data)
	return err
}

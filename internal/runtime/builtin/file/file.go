// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/runtime/executor"
)

const (
	executorType = "file"

	opStat   = "stat"
	opRead   = "read"
	opWrite  = "write"
	opCopy   = "copy"
	opMove   = "move"
	opDelete = "delete"
	opMkdir  = "mkdir"
	opList   = "list"
)

var (
	errConfig      = errors.New("file: configuration error")
	errUnsupported = errors.New("file: unsupported operation")
)

var (
	_ executor.Executor  = (*executorImpl)(nil)
	_ executor.ExitCoder = (*executorImpl)(nil)
)

type executorImpl struct {
	mu       sync.Mutex
	stdout   io.Writer
	stderr   io.Writer
	cfg      config
	op       string
	workDir  string
	exitCode int
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
	Operation   string     `json:"operation"`
	Path        string     `json:"path,omitempty"`
	Source      string     `json:"source,omitempty"`
	Destination string     `json:"destination,omitempty"`
	Exists      *bool      `json:"exists,omitempty"`
	Type        string     `json:"type,omitempty"`
	Size        int64      `json:"size,omitempty"`
	Mode        string     `json:"mode,omitempty"`
	ModTime     *time.Time `json:"modTime,omitempty"`
	Files       int64      `json:"files,omitempty"`
	Directories int64      `json:"directories,omitempty"`
	Bytes       int64      `json:"bytes,omitempty"`
	DryRun      bool       `json:"dryRun,omitempty"`
	Deleted     *bool      `json:"deleted,omitempty"`
	Created     *bool      `json:"created,omitempty"`
	Moved       *bool      `json:"moved,omitempty"`
	Copied      *bool      `json:"copied,omitempty"`
	Content     string     `json:"content,omitempty"`
	Entries     []pathInfo `json:"entries,omitempty"`
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

	env := runtime.GetEnv(ctx)
	return &executorImpl{
		stdout:  os.Stdout,
		stderr:  os.Stderr,
		cfg:     cfg,
		op:      op,
		workDir: env.WorkingDir,
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
	case opStat:
		err = e.runStat()
	case opRead:
		err = e.runRead()
	case opWrite:
		err = e.runWrite()
	case opCopy:
		err = e.runCopy()
	case opMove:
		err = e.runMove()
	case opDelete:
		err = e.runDelete()
	case opMkdir:
		err = e.runMkdir()
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

func (e *executorImpl) runStat() error {
	path, err := e.resolvePath(e.cfg.Path)
	if err != nil {
		return err
	}
	info, err := statPath(path, e.cfg.FollowSymlinks)
	if err != nil {
		if os.IsNotExist(err) && e.cfg.MissingOK {
			exists := false
			return e.writeJSON(opResult{Operation: opStat, Path: path, Exists: &exists})
		}
		return fmt.Errorf("file stat %s: %w", path, err)
	}
	exists := true
	result := infoResult(opStat, path, info)
	result.Exists = &exists
	return e.writeJSON(result)
}

func (e *executorImpl) runRead() error {
	path, err := e.resolvePath(e.cfg.Path)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("file read %s: %w", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("file read %s: cannot read a directory", path)
	}
	if e.cfg.MaxBytes > 0 && info.Size() > e.cfg.MaxBytes {
		return fmt.Errorf("file read %s: file size %d exceeds max_bytes %d", path, info.Size(), e.cfg.MaxBytes)
	}

	if e.cfg.Format == "json" {
		data, err := os.ReadFile(path) //nolint:gosec // path is workflow-controlled local file input.
		if err != nil {
			return fmt.Errorf("file read %s: %w", path, err)
		}
		result := infoResult(opRead, path, info)
		result.Content = string(data)
		result.Bytes = int64(len(data))
		return e.writeJSON(result)
	}

	handle, err := os.Open(path) //nolint:gosec // path is workflow-controlled local file input.
	if err != nil {
		return fmt.Errorf("file read %s: %w", path, err)
	}
	defer func() { _ = handle.Close() }()
	_, err = io.Copy(e.stdout, handle)
	return err
}

func (e *executorImpl) runWrite() error {
	path, err := e.resolvePath(e.cfg.Path)
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
	data := []byte(e.cfg.Content)
	if e.cfg.DryRun {
		created := false
		return e.writeJSON(opResult{Operation: opWrite, Path: path, Bytes: int64(len(data)), DryRun: true, Created: &created})
	}
	if e.cfg.CreateDirs {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return fmt.Errorf("file write %s: create parent directories: %w", path, err)
		}
	}
	if !e.cfg.Overwrite {
		handle, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode) //nolint:gosec // path is workflow-controlled local file output.
		if err != nil {
			return fmt.Errorf("file write %s: %w", path, err)
		}
		if _, err := handle.Write(data); err != nil {
			_ = handle.Close()
			return fmt.Errorf("file write %s: %w", path, err)
		}
		if err := handle.Close(); err != nil {
			return fmt.Errorf("file write %s: %w", path, err)
		}
		created := true
		return e.writeJSON(opResult{Operation: opWrite, Path: path, Bytes: int64(len(data)), Created: &created})
	}
	if e.cfg.Atomic {
		if err := fileutil.WriteFileAtomic(path, data, mode); err != nil {
			return fmt.Errorf("file write %s: %w", path, err)
		}
	} else if err := os.WriteFile(path, data, mode); err != nil { //nolint:gosec // path is workflow-controlled local file output.
		return fmt.Errorf("file write %s: %w", path, err)
	}
	created := true
	return e.writeJSON(opResult{Operation: opWrite, Path: path, Bytes: int64(len(data)), Created: &created})
}

func (e *executorImpl) runCopy() error {
	source, err := e.resolvePath(e.cfg.Source)
	if err != nil {
		return err
	}
	destination, err := e.resolvePath(e.cfg.Destination)
	if err != nil {
		return err
	}
	if samePath(source, destination) {
		return fmt.Errorf("file copy %s to %s: source and destination must differ", source, destination)
	}
	if e.cfg.DryRun {
		copied := false
		return e.writeJSON(opResult{Operation: opCopy, Source: source, Destination: destination, DryRun: true, Copied: &copied})
	}
	stats, err := e.copyPath(source, destination, e.cfg.Recursive)
	if err != nil {
		return err
	}
	copied := true
	return e.writeJSON(opResult{
		Operation:   opCopy,
		Source:      source,
		Destination: destination,
		Files:       stats.files,
		Directories: stats.dirs,
		Bytes:       stats.bytes,
		Copied:      &copied,
	})
}

func (e *executorImpl) runMove() error {
	source, err := e.resolvePath(e.cfg.Source)
	if err != nil {
		return err
	}
	destination, err := e.resolvePath(e.cfg.Destination)
	if err != nil {
		return err
	}
	if samePath(source, destination) {
		return fmt.Errorf("file move %s to %s: source and destination must differ", source, destination)
	}
	if e.cfg.DryRun {
		moved := false
		return e.writeJSON(opResult{Operation: opMove, Source: source, Destination: destination, DryRun: true, Moved: &moved})
	}
	if e.cfg.CreateDirs {
		if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
			return fmt.Errorf("file move %s to %s: create parent directories: %w", source, destination, err)
		}
	}
	sourceInfo, err := os.Lstat(source)
	if err != nil {
		return fmt.Errorf("file move %s to %s: stat source: %w", source, destination, err)
	}
	if !e.cfg.Overwrite {
		if _, err := os.Lstat(destination); err == nil {
			return fmt.Errorf("file move %s to %s: destination exists", source, destination)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("file move %s to %s: stat destination: %w", source, destination, err)
		}
	}
	if err := os.Rename(source, destination); err != nil {
		if errors.Is(err, syscall.EXDEV) {
			if err := e.moveAcrossDevices(source, destination); err != nil {
				return err
			}
		} else if e.cfg.Overwrite && !sourceInfo.IsDir() {
			if err := fileutil.ReplaceFileWithRetry(source, destination); err != nil {
				return fmt.Errorf("file move %s to %s: replace destination: %w", source, destination, err)
			}
		} else {
			return fmt.Errorf("file move %s to %s: %w", source, destination, err)
		}
	}
	moved := true
	return e.writeJSON(opResult{Operation: opMove, Source: source, Destination: destination, Moved: &moved})
}

func (e *executorImpl) runDelete() error {
	path, err := e.resolvePath(e.cfg.Path)
	if err != nil {
		return err
	}
	if isRootPath(path) {
		return fmt.Errorf("file delete %s: refusing to delete filesystem root", path)
	}
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) && e.cfg.MissingOK {
			deleted := false
			return e.writeJSON(opResult{Operation: opDelete, Path: path, Deleted: &deleted})
		}
		return fmt.Errorf("file delete %s: %w", path, err)
	}
	if info.IsDir() && !e.cfg.Recursive {
		return fmt.Errorf("file delete %s: recursive is required to delete a directory", path)
	}
	if e.cfg.DryRun {
		deleted := false
		return e.writeJSON(opResult{Operation: opDelete, Path: path, DryRun: true, Deleted: &deleted})
	}
	if info.IsDir() {
		err = os.RemoveAll(path)
	} else {
		err = os.Remove(path)
	}
	if err != nil {
		return fmt.Errorf("file delete %s: %w", path, err)
	}
	deleted := true
	return e.writeJSON(opResult{Operation: opDelete, Path: path, Deleted: &deleted})
}

func (e *executorImpl) runMkdir() error {
	path, err := e.resolvePath(e.cfg.Path)
	if err != nil {
		return err
	}
	mode := os.FileMode(0o750)
	if e.cfg.Mode != "" {
		mode, err = parseFileMode(e.cfg.Mode)
		if err != nil {
			return err
		}
	}
	if e.cfg.DryRun {
		created := false
		return e.writeJSON(opResult{Operation: opMkdir, Path: path, DryRun: true, Created: &created})
	}
	if err := os.MkdirAll(path, mode); err != nil {
		return fmt.Errorf("file mkdir %s: %w", path, err)
	}
	created := true
	return e.writeJSON(opResult{Operation: opMkdir, Path: path, Created: &created})
}

func (e *executorImpl) runList() error {
	root, err := e.resolvePath(e.cfg.Path)
	if err != nil {
		return err
	}
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("file list %s: %w", root, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("file list %s: path must be a directory", root)
	}

	entries, err := e.listEntries(root)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Path < entries[j].Path
	})
	return e.writeJSON(opResult{Operation: opList, Path: root, Entries: entries, Files: countFiles(entries)})
}

func (e *executorImpl) listEntries(root string) ([]pathInfo, error) {
	var entries []pathInfo
	addEntry := func(path string, info fs.FileInfo) error {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
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
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
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

	children, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, child := range children {
		info, err := child.Info()
		if err != nil {
			return nil, err
		}
		if err := addEntry(filepath.Join(root, child.Name()), info); err != nil {
			return nil, err
		}
	}
	return entries, nil
}

type copyStats struct {
	files int64
	dirs  int64
	bytes int64
}

func (e *executorImpl) copyPath(source, destination string, recursive bool) (copyStats, error) {
	info, err := statPath(source, e.cfg.FollowSymlinks)
	if err != nil {
		return copyStats{}, fmt.Errorf("file copy %s to %s: %w", source, destination, err)
	}
	if e.cfg.FollowSymlinks {
		resolved, err := filepath.EvalSymlinks(source)
		if err != nil {
			return copyStats{}, fmt.Errorf("file copy %s to %s: resolve source symlink: %w", source, destination, err)
		}
		source = resolved
	}
	if samePath(source, destination) {
		return copyStats{}, fmt.Errorf("file copy %s to %s: source and destination must differ", source, destination)
	}
	if info.IsDir() {
		if !recursive {
			return copyStats{}, fmt.Errorf("file copy %s to %s: recursive is required to copy a directory", source, destination)
		}
		inside, err := pathInsideOrSame(source, destination)
		if err != nil {
			return copyStats{}, fmt.Errorf("file copy %s to %s: compare paths: %w", source, destination, err)
		}
		if inside {
			return copyStats{}, fmt.Errorf("file copy %s to %s: destination must not be inside source", source, destination)
		}
		return e.copyDir(source, destination)
	}
	bytes, err := e.copyLeaf(source, destination, info)
	if err != nil {
		return copyStats{}, err
	}
	return copyStats{files: 1, bytes: bytes}, nil
}

func (e *executorImpl) copyDir(source, destination string) (copyStats, error) {
	var stats copyStats
	err := filepath.WalkDir(source, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if rel == "." {
			return e.ensureDir(target, info.Mode().Perm())
		}
		if info.IsDir() {
			if err := e.ensureDir(target, info.Mode().Perm()); err != nil {
				return err
			}
			stats.dirs++
			return nil
		}
		bytes, err := e.copyLeaf(path, target, info)
		if err != nil {
			return err
		}
		stats.files++
		stats.bytes += bytes
		return nil
	})
	return stats, err
}

func (e *executorImpl) copyLeaf(source, destination string, info fs.FileInfo) (int64, error) {
	if e.cfg.CreateDirs {
		if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
			return 0, fmt.Errorf("file copy %s to %s: create parent directories: %w", source, destination, err)
		}
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return 0, e.copySymlink(source, destination)
	}
	if !info.Mode().IsRegular() {
		return 0, fmt.Errorf("file copy %s to %s: source must be a regular file or directory", source, destination)
	}
	return e.copyRegularFile(source, destination, info.Mode().Perm())
}

func (e *executorImpl) copyRegularFile(source, destination string, mode os.FileMode) (int64, error) {
	src, err := os.Open(source) //nolint:gosec // path is workflow-controlled local file input.
	if err != nil {
		return 0, fmt.Errorf("file copy %s to %s: open source: %w", source, destination, err)
	}
	defer func() { _ = src.Close() }()

	if e.cfg.Overwrite {
		return copyRegularFileAtomically(src, source, destination, mode)
	}

	dst, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode) //nolint:gosec // path is workflow-controlled local file output.
	if err != nil {
		return 0, fmt.Errorf("file copy %s to %s: open destination: %w", source, destination, err)
	}
	defer func() { _ = dst.Close() }()

	written, err := io.Copy(dst, src)
	if err != nil {
		return written, fmt.Errorf("file copy %s to %s: %w", source, destination, err)
	}
	return written, nil
}

func copyRegularFileAtomically(src *os.File, source, destination string, mode os.FileMode) (int64, error) {
	tmp, err := os.CreateTemp(filepath.Dir(destination), "."+filepath.Base(destination)+".tmp.*") //nolint:gosec // path is workflow-controlled local file output.
	if err != nil {
		return 0, fmt.Errorf("file copy %s to %s: create temporary destination: %w", source, destination, err)
	}
	tmpPath := tmp.Name()
	removeTmp := true
	defer func() {
		if removeTmp {
			_ = os.Remove(tmpPath)
		}
	}()

	written, err := io.Copy(tmp, src)
	if err != nil {
		_ = tmp.Close()
		return written, fmt.Errorf("file copy %s to %s: %w", source, destination, err)
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return written, fmt.Errorf("file copy %s to %s: chmod temporary destination: %w", source, destination, err)
	}
	if err := tmp.Close(); err != nil {
		return written, fmt.Errorf("file copy %s to %s: close temporary destination: %w", source, destination, err)
	}
	if err := fileutil.ReplaceFileWithRetry(tmpPath, destination); err != nil {
		return written, fmt.Errorf("file copy %s to %s: replace destination: %w", source, destination, err)
	}
	removeTmp = false
	return written, nil
}

func (e *executorImpl) copySymlink(source, destination string) error {
	target, err := os.Readlink(source)
	if err != nil {
		return fmt.Errorf("file copy %s to %s: read symlink: %w", source, destination, err)
	}
	if _, err := os.Lstat(destination); err == nil {
		if !e.cfg.Overwrite {
			return fmt.Errorf("file copy %s to %s: destination exists", source, destination)
		}
		if err := os.RemoveAll(destination); err != nil {
			return fmt.Errorf("file copy %s to %s: remove destination: %w", source, destination, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("file copy %s to %s: stat destination: %w", source, destination, err)
	}
	if err := os.Symlink(target, destination); err != nil {
		return fmt.Errorf("file copy %s to %s: create symlink: %w", source, destination, err)
	}
	return nil
}

func (e *executorImpl) moveAcrossDevices(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return fmt.Errorf("file move %s to %s: stat source: %w", source, destination, err)
	}
	if info.IsDir() && !e.cfg.Recursive {
		return fmt.Errorf("file move %s to %s: recursive is required to move a directory across filesystems", source, destination)
	}
	if info.IsDir() && e.cfg.Overwrite {
		if _, err := os.Lstat(destination); err == nil {
			return fmt.Errorf("file move %s to %s: cross-filesystem directory overwrite is not supported", source, destination)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("file move %s to %s: stat destination: %w", source, destination, err)
		}
	}
	if _, err := e.copyPath(source, destination, info.IsDir()); err != nil {
		return err
	}
	if info.IsDir() {
		if err := os.RemoveAll(source); err != nil {
			return fmt.Errorf("file move %s to %s: remove source: %w", source, destination, err)
		}
		return nil
	}
	if err := os.Remove(source); err != nil {
		return fmt.Errorf("file move %s to %s: remove source: %w", source, destination, err)
	}
	return nil
}

func (e *executorImpl) ensureDir(path string, mode os.FileMode) error {
	if info, err := os.Stat(path); err == nil {
		if info.IsDir() {
			return nil
		}
		return fmt.Errorf("file copy: destination %s exists and is not a directory", path)
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.MkdirAll(path, mode)
}

func (e *executorImpl) resolvePath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("%w: path must not be empty", errConfig)
	}
	if filepath.IsAbs(raw) || strings.HasPrefix(raw, "~") {
		resolved, err := fileutil.ResolvePath(raw)
		if err != nil {
			return "", err
		}
		return resolved, nil
	}
	workDir := e.workDir
	if strings.TrimSpace(workDir) == "" {
		var err error
		workDir, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve working directory: %w", err)
		}
	}
	return filepath.Clean(filepath.Join(workDir, raw)), nil
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

func statPath(path string, followSymlinks bool) (fs.FileInfo, error) {
	if followSymlinks {
		return os.Stat(path)
	}
	return os.Lstat(path)
}

func samePath(a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if goruntime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func pathInsideOrSame(parent, child string) (bool, error) {
	parentAbs, err := canonicalPath(parent)
	if err != nil {
		return false, err
	}
	childAbs, err := canonicalPath(child)
	if err != nil {
		return false, err
	}
	if samePath(parentAbs, childAbs) {
		return true, nil
	}
	rel, err := filepath.Rel(parentAbs, childAbs)
	if err != nil {
		return false, err
	}
	if rel == "." {
		return true, nil
	}
	if rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false, nil
	}
	return true, nil
}

func canonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		return filepath.Clean(resolved), nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}

	var missing []string
	current := filepath.Clean(abs)
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}

		parent := filepath.Dir(current)
		if parent == current {
			return filepath.Clean(abs), nil
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

func infoResult(operation, path string, info fs.FileInfo) opResult {
	modTime := info.ModTime()
	return opResult{
		Operation: operation,
		Path:      path,
		Type:      fileType(info),
		Size:      info.Size(),
		Mode:      info.Mode().String(),
		ModTime:   &modTime,
	}
}

func newPathInfo(path string, info fs.FileInfo) pathInfo {
	return pathInfo{
		Path:      path,
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

func isRootPath(path string) bool {
	clean := filepath.Clean(path)
	volume := filepath.VolumeName(clean)
	root := volume + string(filepath.Separator)
	return clean == root
}

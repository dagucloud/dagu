// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package dagdiscovery enumerates DAG definition files and watchable directories.
package dagdiscovery

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

// ErrExternalSymlinkDisabled indicates that a DAG file symlink needs the discovery opt-in.
var ErrExternalSymlinkDisabled = errors.New("external DAG file symlinks are disabled")

// File describes a discovered DAG definition.
type File struct {
	RelPath      string
	ResolvedPath string
	Size         int64
	ModTime      int64
}

// Options controls which DAG definition files are discoverable.
type Options struct {
	Recursive bool
	Symlinks  bool
}

// ResolvedFile describes a DAG file entry and its canonical read target.
type ResolvedFile struct {
	EntryPath       string
	ResolvedPath    string
	Symlink         bool
	ExternalSymlink bool
}

// Result contains discoverable files, directories, and non-fatal traversal errors.
type Result struct {
	Files  []File
	Dirs   []string
	Errors []error
}

// Scan enumerates DAG definitions beneath root.
func Scan(root string, opts Options) (Result, error) {
	root = filepath.Clean(root)
	if !opts.Recursive {
		return scanRoot(root, opts.Symlinks)
	}

	walkRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return Result{}, err
	}
	info, err := os.Stat(walkRoot)
	if err != nil {
		return Result{}, err
	}
	if !info.IsDir() {
		return Result{}, fmt.Errorf("%s is not a directory", root)
	}

	result := Result{}
	err = filepath.WalkDir(walkRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		relPath, err := filepath.Rel(walkRoot, path)
		if err != nil {
			return err
		}
		relPath = filepath.ToSlash(relPath)

		if walkErr != nil {
			if path == walkRoot {
				return walkErr
			}
			result.Errors = append(result.Errors, fmt.Errorf("%s: %w", relPath, walkErr))
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if path != walkRoot && entry.Type()&os.ModeSymlink != 0 {
			if !fileutil.IsYAMLFile(entry.Name()) {
				return nil
			}
			resolved, err := ResolveFile(root, relPath)
			if err != nil {
				result.Errors = append(result.Errors, fmt.Errorf("%s: %w", relPath, err))
				return nil
			}
			if !opts.Symlinks {
				if resolved.ExternalSymlink {
					result.Errors = append(result.Errors, externalSymlinkDisabledError(relPath))
				}
				return nil
			}
			appendResolvedFile(&result, relPath, resolved)
			return nil
		}

		if entry.IsDir() {
			if path != walkRoot &&
				(strings.HasPrefix(entry.Name(), ".") || relPath == workspace.BaseConfigDirName) {
				return filepath.SkipDir
			}
			dir := root
			if relPath != "." {
				dir = filepath.Join(root, filepath.FromSlash(relPath))
			}
			result.Dirs = append(result.Dirs, dir)
			return nil
		}
		if !fileutil.IsYAMLFile(entry.Name()) {
			return nil
		}

		resolved, err := ResolveFile(root, relPath)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("%s: %w", relPath, err))
			return nil
		}
		appendResolvedFile(&result, relPath, resolved)
		return nil
	})
	if err != nil {
		return Result{}, err
	}

	sortResult(&result)
	return result, nil
}

func scanRoot(root string, symlinks bool) (Result, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return Result{}, err
	}

	result := Result{Dirs: []string{root}}
	for _, entry := range entries {
		if entry.IsDir() || !fileutil.IsYAMLFile(entry.Name()) {
			continue
		}
		resolved, err := ResolveFile(root, entry.Name())
		if err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("%s: %w", entry.Name(), err))
			continue
		}
		if resolved.ExternalSymlink && !symlinks {
			result.Errors = append(result.Errors, externalSymlinkDisabledError(entry.Name()))
			continue
		}
		appendResolvedFile(&result, filepath.ToSlash(entry.Name()), resolved)
	}

	sortResult(&result)
	return result, nil
}

// ResolveFile resolves one lexically contained DAG entry without allowing
// symlinked directory components beneath root.
func ResolveFile(root, relPath string) (ResolvedFile, error) {
	entryPath, err := fileutil.ResolvePathWithinBase(root, relPath)
	if err != nil {
		return ResolvedFile{}, err
	}

	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return ResolvedFile{}, err
	}
	if err := rejectSymlinkedParentDirectories(rootAbs, entryPath); err != nil {
		return ResolvedFile{}, err
	}

	realRoot, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return ResolvedFile{}, err
	}
	realRoot, err = filepath.Abs(realRoot)
	if err != nil {
		return ResolvedFile{}, err
	}

	entryInfo, err := os.Lstat(entryPath)
	if err != nil {
		return ResolvedFile{}, err
	}
	symlink := entryInfo.Mode()&os.ModeSymlink != 0

	resolvedPath, err := filepath.EvalSymlinks(entryPath)
	if err != nil {
		return ResolvedFile{}, err
	}
	resolvedPath, err = filepath.Abs(resolvedPath)
	if err != nil {
		return ResolvedFile{}, err
	}
	info, err := os.Stat(resolvedPath)
	if err != nil {
		return ResolvedFile{}, err
	}
	if !info.Mode().IsRegular() {
		return ResolvedFile{}, fmt.Errorf("DAG definition is not a regular file")
	}

	external := !pathWithinRoot(resolvedPath, realRoot)
	if external && !symlink {
		return ResolvedFile{}, fileutil.ErrPathEscapesBase
	}
	return ResolvedFile{
		EntryPath:       entryPath,
		ResolvedPath:    resolvedPath,
		Symlink:         symlink,
		ExternalSymlink: external,
	}, nil
}

func rejectSymlinkedParentDirectories(rootAbs, entryPath string) error {
	relParent, err := filepath.Rel(rootAbs, filepath.Dir(entryPath))
	if err != nil || relParent == ".." || strings.HasPrefix(relParent, ".."+string(filepath.Separator)) {
		return fileutil.ErrPathEscapesBase
	}
	if relParent == "." {
		return nil
	}

	current := rootAbs
	for component := range strings.SplitSeq(relParent, string(filepath.Separator)) {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fileutil.ErrPathEscapesBase
		}
		if !info.IsDir() {
			return fmt.Errorf("%s is not a directory", current)
		}
	}
	return nil
}

func pathWithinRoot(path, root string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func appendResolvedFile(result *Result, relPath string, resolved ResolvedFile) {
	info, err := os.Stat(resolved.ResolvedPath)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Errorf("%s: %w", relPath, err))
		return
	}
	result.Files = append(result.Files, File{
		RelPath:      filepath.ToSlash(relPath),
		ResolvedPath: resolved.ResolvedPath,
		Size:         info.Size(),
		ModTime:      info.ModTime().UnixNano(),
	})
}

func externalSymlinkDisabledError(relPath string) error {
	return fmt.Errorf(
		"%s: DAG file symlink resolves outside the configured DAG directory; enable dag_discovery.symlinks to load it: %w",
		filepath.ToSlash(relPath),
		ErrExternalSymlinkDisabled,
	)
}

func sortResult(result *Result) {
	sort.Slice(result.Files, func(i, j int) bool {
		return result.Files[i].RelPath < result.Files[j].RelPath
	})
	sort.Strings(result.Dirs)
	sort.Slice(result.Errors, func(i, j int) bool {
		return result.Errors[i].Error() < result.Errors[j].Error()
	})
}

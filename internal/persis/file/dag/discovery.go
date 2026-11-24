// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

// ErrExternalSymlinkDisabled indicates that a DAG file symlink needs the discovery opt-in.
var ErrExternalSymlinkDisabled = errors.New("external DAG file symlinks are disabled")

// DiscoveredFile describes a discovered DAG definition.
type DiscoveredFile struct {
	RelPath      string
	ResolvedPath string
	Size         int64
	ModTime      int64
}

// DiscoveryOptions controls which DAG definition files are discoverable.
type DiscoveryOptions struct {
	Recursive bool
	Symlinks  bool
	// SkipDirs names directories directly under root that recursive
	// discovery omits, so child-DAG subtrees stay resolvable by path but out
	// of the catalog and scheduler. A name deeper in the tree is not skipped.
	SkipDirs []string
	// SkipRoots lists directories that recursive discovery omits wherever
	// it meets them below root, such as another owner's DAG directory
	// nested in this one. A directory matches by identity, whatever the
	// case or links of its path.
	SkipRoots []string
}

// ResolvedFile describes a DAG file entry and its canonical read target.
type ResolvedFile struct {
	EntryPath       string
	ResolvedPath    string
	Symlink         bool
	ExternalSymlink bool
}

// DiscoveryResult contains discoverable files, directories, and non-fatal traversal errors.
type DiscoveryResult struct {
	Files  []DiscoveredFile
	Dirs   []string
	Errors []error
}

// Discover enumerates DAG definitions beneath root.
func Discover(root string, opts DiscoveryOptions) (DiscoveryResult, error) {
	root = filepath.Clean(root)
	if !opts.Recursive {
		return scanRoot(root, opts.Symlinks)
	}

	walkRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return DiscoveryResult{}, err
	}
	info, err := os.Stat(walkRoot)
	if err != nil {
		return DiscoveryResult{}, err
	}
	if !info.IsDir() {
		return DiscoveryResult{}, fmt.Errorf("%s is not a directory", root)
	}

	skipRoots, skipErrs := statSkipRoots(opts.SkipRoots)
	result := DiscoveryResult{Errors: skipErrs}
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
			if !isDAGFile(entry.Name()) {
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
			if relPath != "." {
				if strings.HasPrefix(entry.Name(), ".") ||
					relPath == workspace.BaseConfigDirName ||
					slices.Contains(opts.SkipDirs, relPath) ||
					isSkipRoot(skipRoots, path, entry.Name()) {
					return filepath.SkipDir
				}
			}
			dir := root
			if relPath != "." {
				dir = filepath.Join(root, filepath.FromSlash(relPath))
			}
			result.Dirs = append(result.Dirs, dir)
			return nil
		}
		if !isDAGFile(entry.Name()) {
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
		return DiscoveryResult{}, err
	}

	sortResult(&result)
	return result, nil
}

// skipRoot is an existing directory that recursive discovery omits.
type skipRoot struct {
	name string
	info os.FileInfo
}

// statSkipRoots returns the directories among dirs that exist now. One
// created later is omitted from the next discovery on. A directory that
// cannot be looked up for a reason other than its absence is reported, since
// discovery may then read what it should skip.
func statSkipRoots(dirs []string) ([]skipRoot, []error) {
	var (
		roots []skipRoot
		errs  []error
	)
	for _, dir := range dirs {
		info, resolved, err := statResolved(dir)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			errs = append(errs, fmt.Errorf("skip %s: %w", dir, err))
			continue
		case !info.IsDir():
			continue
		}
		roots = append(roots, skipRoot{name: filepath.Base(resolved), info: info})
	}
	return roots, errs
}

// underSkipRoot reports whether path lies in one of roots below dir.
func underSkipRoot(roots []skipRoot, dir, path string) bool {
	if len(roots) == 0 {
		return false
	}
	for current := filepath.Dir(path); current != dir; {
		if isSkipRoot(roots, current, filepath.Base(current)) {
			return true
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return false
}

func statResolved(path string) (os.FileInfo, string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, "", err
	}
	info, err := os.Stat(resolved)
	return info, resolved, err
}

// isSkipRoot reports whether the directory at path, called name, is one of
// roots. Only a directory whose name matches a root's without regard to case
// is compared by identity, so other directories cost no system call.
func isSkipRoot(roots []skipRoot, path, name string) bool {
	for _, root := range roots {
		if !strings.EqualFold(name, root.name) {
			continue
		}
		if info, err := os.Stat(path); err == nil && os.SameFile(info, root.info) {
			return true
		}
	}
	return false
}

func scanRoot(root string, symlinks bool) (DiscoveryResult, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return DiscoveryResult{}, err
	}

	result := DiscoveryResult{Dirs: []string{root}}
	for _, entry := range entries {
		if entry.IsDir() || !isDAGFile(entry.Name()) {
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

// isDAGFile reports whether a file name can hold a DAG: a YAML file that is
// not a test file.
func isDAGFile(name string) bool {
	return fileutil.IsYAMLFile(name) && !spec.IsTestFile(name)
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

func appendResolvedFile(result *DiscoveryResult, relPath string, resolved ResolvedFile) {
	info, err := os.Stat(resolved.ResolvedPath)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Errorf("%s: %w", relPath, err))
		return
	}
	result.Files = append(result.Files, DiscoveredFile{
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

func sortResult(result *DiscoveryResult) {
	sort.Slice(result.Files, func(i, j int) bool {
		return result.Files[i].RelPath < result.Files[j].RelPath
	})
	sort.Strings(result.Dirs)
	sort.Slice(result.Errors, func(i, j int) bool {
		return result.Errors[i].Error() < result.Errors[j].Error()
	})
}

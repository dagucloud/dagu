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
	// SkipRoots lists directories that discovery omits wherever it meets
	// them below root, such as another owner's DAG directory nested in this
	// one, along with file symlinks into them. A directory matches by
	// identity, whatever the case or links of its path.
	SkipRoots []string
	// ConfineSymlinks omits every file symlink whose target lies outside
	// root, whatever Symlinks says.
	ConfineSymlinks bool
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
	return discover(root, opts, isDAGFile)
}

// discover enumerates the files beneath root whose names match, under the
// rules Discover applies to DAG definitions.
func discover(root string, opts DiscoveryOptions, match func(name string) bool) (DiscoveryResult, error) {
	root = filepath.Clean(root)
	if !opts.Recursive {
		return scanRoot(root, opts, match)
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
	realRoot, err := realDir(skipRoots, root)
	if err != nil {
		return DiscoveryResult{}, err
	}
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
			if !match(entry.Name()) {
				return nil
			}
			resolved, err := ResolveFile(root, relPath)
			if err != nil {
				result.Errors = append(result.Errors, fmt.Errorf("%s: %w", relPath, err))
				return nil
			}
			if !opts.Symlinks || resolved.ExternalSymlink && opts.ConfineSymlinks {
				if resolved.ExternalSymlink {
					result.Errors = append(result.Errors, externalSymlinkError(relPath, opts.ConfineSymlinks))
				}
				return nil
			}
			// The walk has already skipped the skip roots above the entry.
			if targetInSkipRoot(skipRoots, realRoot, resolved) {
				return nil
			}
			appendResolvedFile(&result, relPath, resolved)
			return nil
		}

		if entry.IsDir() {
			if relPath != "." {
				if strings.HasPrefix(entry.Name(), ".") ||
					relPath == workspace.BaseConfigDirName ||
					isSkillsDir(walkRoot, path, relPath) ||
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
		if !match(entry.Name()) {
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

// isSkillsDir reports whether the directory at path, at relPath below root,
// is the reserved skills directory. A name that matches only without regard
// to case is compared by identity, since a file system that ignores case
// serves that directory for root/skills.
func isSkillsDir(root, path, relPath string) bool {
	if relPath == spec.SkillsDirName {
		return true
	}
	if !strings.EqualFold(relPath, spec.SkillsDirName) {
		return false
	}
	reserved, err := os.Stat(filepath.Join(root, spec.SkillsDirName))
	if err != nil {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && os.SameFile(info, reserved)
}

// skipRoot is an existing directory that discovery and lookups by path omit.
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

// realDir returns dir, absolute with its links resolved, for comparing file
// symlink targets against roots. It returns "" when roots is empty.
func realDir(roots []skipRoot, dir string) (string, error) {
	if len(roots) == 0 {
		return "", nil
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	return filepath.Abs(resolved)
}

// inSkipRoot reports whether resolved, looked up by path below absDir, lies
// in one of roots or is a file symlink into one.
func inSkipRoot(roots []skipRoot, absDir string, resolved ResolvedFile) bool {
	if underSkipRoot(roots, absDir, resolved.EntryPath) {
		return true
	}
	if !resolved.Symlink {
		return false
	}
	realAbsDir, err := realDir(roots, absDir)
	return err == nil && targetInSkipRoot(roots, realAbsDir, resolved)
}

// targetInSkipRoot reports whether resolved is a file symlink whose target
// lies in one of roots below realRoot, the scanned directory with its links
// resolved. A target outside realRoot is left to the symlink rules.
func targetInSkipRoot(roots []skipRoot, realRoot string, resolved ResolvedFile) bool {
	return resolved.Symlink && underSkipRoot(roots, realRoot, resolved.ResolvedPath)
}

// underSkipRoot reports whether path lies in one of roots below dir. Every
// ancestor is compared by identity, since a path given to a lookup may spell
// a root's name otherwise, such as by a Windows short name.
func underSkipRoot(roots []skipRoot, dir, path string) bool {
	if len(roots) == 0 {
		return false
	}
	for current := filepath.Dir(path); strictlyWithin(current, dir); current = filepath.Dir(current) {
		if sameAsSkipRoot(roots, current) {
			return true
		}
	}
	return false
}

// strictlyWithin reports whether path lies below dir.
func strictlyWithin(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func statResolved(path string) (os.FileInfo, string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, "", err
	}
	info, err := os.Stat(resolved)
	return info, resolved, err
}

// isSkipRoot reports whether the directory at path, listed in its parent as
// name, is one of roots. Only a directory whose name matches a root's without
// regard to case is compared by identity, so other directories cost no
// system call.
func isSkipRoot(roots []skipRoot, path, name string) bool {
	for _, root := range roots {
		if strings.EqualFold(name, root.name) {
			return sameAsSkipRoot(roots, path)
		}
	}
	return false
}

// sameAsSkipRoot reports whether the directory at path is one of roots.
func sameAsSkipRoot(roots []skipRoot, path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return slices.ContainsFunc(roots, func(root skipRoot) bool {
		return os.SameFile(info, root.info)
	})
}

func scanRoot(root string, opts DiscoveryOptions, match func(name string) bool) (DiscoveryResult, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return DiscoveryResult{}, err
	}

	skipRoots, skipErrs := statSkipRoots(opts.SkipRoots)
	realRoot, err := realDir(skipRoots, root)
	if err != nil {
		return DiscoveryResult{}, err
	}
	result := DiscoveryResult{Dirs: []string{root}, Errors: skipErrs}
	for _, entry := range entries {
		if entry.IsDir() || !match(entry.Name()) {
			continue
		}
		resolved, err := ResolveFile(root, entry.Name())
		if err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("%s: %w", entry.Name(), err))
			continue
		}
		if resolved.ExternalSymlink && (!opts.Symlinks || opts.ConfineSymlinks) {
			result.Errors = append(result.Errors, externalSymlinkError(entry.Name(), opts.ConfineSymlinks))
			continue
		}
		// An entry of root lies below no skip root; only its target can.
		if targetInSkipRoot(skipRoots, realRoot, resolved) {
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

// externalSymlinkError reports a file symlink that discovery does not
// follow out of its DAG directory. A confined directory follows none, so
// no setting loads it.
func externalSymlinkError(relPath string, confined bool) error {
	return &skippedSymlinkError{relPath: filepath.ToSlash(relPath), confined: confined}
}

// skippedSymlinkError is a file symlink that discovery passed over because
// it leads out of its DAG directory.
type skippedSymlinkError struct {
	relPath  string
	confined bool
}

func (e *skippedSymlinkError) Error() string {
	hint := "enable dag_discovery.symlinks to load it"
	if e.confined {
		hint = "a project never follows a symbolic link out of its DAG directory"
	}
	return fmt.Sprintf("%s: DAG file symlink resolves outside the configured DAG directory; %s: %s",
		e.relPath, hint, ErrExternalSymlinkDisabled)
}

func (e *skippedSymlinkError) Unwrap() error { return ErrExternalSymlinkDisabled }

func sortResult(result *DiscoveryResult) {
	sort.Slice(result.Files, func(i, j int) bool {
		return result.Files[i].RelPath < result.Files[j].RelPath
	})
	sort.Strings(result.Dirs)
	sort.Slice(result.Errors, func(i, j int) bool {
		return result.Errors[i].Error() < result.Errors[j].Error()
	})
}

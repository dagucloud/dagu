// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intake

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

func copyWorkDir(sourceWorkDir, targetWorkDir string) error {
	sourceWorkDir = cleanWorkDir(sourceWorkDir)
	targetWorkDir = cleanWorkDir(targetWorkDir)
	if sourceWorkDir == "" || targetWorkDir == "" || sourceWorkDir == targetWorkDir {
		return nil
	}

	info, err := os.Stat(sourceWorkDir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", sourceWorkDir)
	}
	if err := os.MkdirAll(targetWorkDir, 0o750); err != nil {
		return err
	}

	// Directories are created writable so their contents can be copied in;
	// their source permissions are applied once the walk has finished.
	type dirMode struct {
		path string
		mode fs.FileMode
	}
	var dirs []dirMode
	err = filepath.WalkDir(sourceWorkDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(sourceWorkDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		targetPath := filepath.Join(targetWorkDir, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		mode := info.Mode()
		switch {
		case entry.IsDir():
			dirs = append(dirs, dirMode{path: targetPath, mode: mode.Perm()})
			return os.MkdirAll(targetPath, 0o750)
		case mode.Type()&os.ModeSymlink != 0:
			return copyWorkDirSymlink(sourceWorkDir, targetWorkDir, path, targetPath)
		case mode.IsRegular():
			return copyWorkDirFile(path, targetPath, mode)
		default:
			return nil
		}
	})
	if err != nil {
		return err
	}
	for _, dir := range slices.Backward(dirs) {
		if err := os.Chmod(dir.path, dir.mode); err != nil {
			return err
		}
	}
	return nil
}

func copyWorkDirSymlink(sourceWorkDir, targetWorkDir, sourcePath, targetPath string) error {
	linkTarget, err := os.Readlink(sourcePath)
	if err != nil {
		return err
	}

	resolvedSourceTarget := linkTarget
	if !filepath.IsAbs(resolvedSourceTarget) {
		resolvedSourceTarget = filepath.Join(filepath.Dir(sourcePath), resolvedSourceTarget)
	}
	resolvedSourceTarget = filepath.Clean(resolvedSourceTarget)
	if err := ensurePathWithin(sourceWorkDir, resolvedSourceTarget); err != nil {
		return fmt.Errorf("unsafe symlink target %s: %w", sourcePath, err)
	}
	if evaluatedSourceTarget, err := filepath.EvalSymlinks(resolvedSourceTarget); err == nil {
		if err := ensureResolvedPathWithin(sourceWorkDir, evaluatedSourceTarget); err != nil {
			return fmt.Errorf("unsafe symlink target %s: %w", sourcePath, err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	sourceTargetRel, err := filepath.Rel(sourceWorkDir, resolvedSourceTarget)
	if err != nil {
		return err
	}
	targetLinkTarget := filepath.Join(targetWorkDir, sourceTargetRel)
	relativeTargetLink, err := filepath.Rel(filepath.Dir(targetPath), targetLinkTarget)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(targetPath), 0o750); err != nil {
		return err
	}
	if err := os.Remove(targetPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Symlink(relativeTargetLink, targetPath) //nolint:gosec // symlink target is constrained to the copied work directory.
}

func ensurePathWithin(baseDir, targetPath string) error {
	baseAbs, err := filepath.Abs(baseDir)
	if err != nil {
		return err
	}
	targetAbs, err := filepath.Abs(targetPath)
	if err != nil {
		return err
	}
	relToBase, err := filepath.Rel(baseAbs, targetAbs)
	if err != nil {
		return err
	}
	if relToBase == ".." || strings.HasPrefix(relToBase, ".."+string(filepath.Separator)) || filepath.IsAbs(relToBase) {
		return fmt.Errorf("path escapes source work directory")
	}
	return nil
}

func ensureResolvedPathWithin(baseDir, targetPath string) error {
	resolvedBase, err := filepath.EvalSymlinks(baseDir)
	if err != nil {
		return err
	}
	return ensurePathWithin(resolvedBase, targetPath)
}

func copyWorkDirFile(sourcePath, targetPath string, mode fs.FileMode) error {
	source, err := os.Open(sourcePath) //nolint:gosec
	if err != nil {
		return err
	}
	defer func() {
		_ = source.Close()
	}()

	if err := os.MkdirAll(filepath.Dir(targetPath), 0o750); err != nil {
		return err
	}
	target, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode.Perm()) //nolint:gosec
	if err != nil {
		return err
	}
	defer func() {
		_ = target.Close()
	}()

	if _, err := io.Copy(target, source); err != nil {
		return err
	}
	return target.Chmod(mode.Perm())
}

func remapWorkDirOutputs(nodes []*ir.Node, sourceWorkDir, targetWorkDir string) {
	sourceWorkDir = cleanWorkDir(sourceWorkDir)
	targetWorkDir = cleanWorkDir(targetWorkDir)
	if sourceWorkDir == "" || targetWorkDir == "" || sourceWorkDir == targetWorkDir {
		return
	}

	replacements := [][2]string{{sourceWorkDir, targetWorkDir}}
	sourceSlash := filepath.ToSlash(sourceWorkDir)
	targetSlash := filepath.ToSlash(targetWorkDir)
	if sourceSlash != sourceWorkDir || targetSlash != targetWorkDir {
		replacements = append(replacements, [2]string{sourceSlash, targetSlash})
	}

	for _, node := range nodes {
		if node == nil || !node.SkippedByRetry || node.OutputVariables == nil {
			continue
		}
		node.OutputVariables.Range(func(key, value any) bool {
			text, ok := value.(string)
			if !ok {
				return true
			}
			rewritten := text
			for _, replacement := range replacements {
				rewritten = strings.ReplaceAll(rewritten, replacement[0], replacement[1])
			}
			if rewritten != text {
				node.OutputVariables.Store(key, rewritten)
			}
			return true
		})
	}
}

func cleanWorkDir(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return ""
	}
	if abs, err := filepath.Abs(dir); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(dir)
}

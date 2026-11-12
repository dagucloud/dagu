// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/workspace"
	"github.com/fsnotify/fsnotify"
)

type baseWatchPath struct {
	path      string
	workspace bool
}

func (er *entryReaderImpl) addInitialWatches(dirs []string) error {
	dirs, err := er.withBaseWatchDirs(dirs)
	if err != nil {
		return err
	}
	for _, dir := range dirs {
		if _, exists := er.watchedDirs[dir]; exists {
			continue
		}
		if err := er.watcher.Add(dir); err != nil {
			return fmt.Errorf("failed to watch directory %s: %w", dir, err)
		}
		er.watchedDirs[dir] = struct{}{}
	}
	return nil
}

// Parent watches survive atomic replacement and discover optional base files.
func (er *entryReaderImpl) withBaseWatchDirs(dirs []string) ([]string, error) {
	basePaths := []baseWatchPath{{path: er.baseConfigPath}, {path: er.workspaceBaseConfigDir, workspace: true}}
	for _, path := range []string{er.baseConfigPath, er.workspaceBaseConfigDir} {
		if path == "" {
			continue
		}
		dir, err := existingWatchDir(filepath.Dir(path))
		if err != nil {
			return nil, err
		}
		dirs = append(dirs, dir)
	}
	if er.calendarsDir != "" {
		// An event here makes the reader compare calendar file state. The
		// calendars are optional, so an unusable path drops only this watch.
		if dir, err := existingWatchDir(er.calendarsDir); err == nil {
			dirs = append(dirs, dir)
		}
	}
	if er.workspaceBaseConfigDir == "" {
		return er.withBaseTargetDirs(dirs, basePaths), nil
	}
	entries, err := os.ReadDir(er.workspaceBaseConfigDir)
	if os.IsNotExist(err) {
		return er.withBaseTargetDirs(dirs, basePaths), nil
	}
	if err != nil {
		return nil, err
	}
	dirs = append(dirs, er.workspaceBaseConfigDir)
	for _, entry := range entries {
		if entry.IsDir() && workspace.ValidateName(entry.Name()) == nil {
			dir := filepath.Join(er.workspaceBaseConfigDir, entry.Name())
			dirs = append(dirs, dir)
			basePaths = append(basePaths, baseWatchPath{path: filepath.Join(dir, workspace.BaseConfigFileName)})
		}
	}
	return er.withBaseTargetDirs(dirs, basePaths), nil
}

func (er *entryReaderImpl) withBaseTargetDirs(dirs []string, basePaths []baseWatchPath) []string {
	er.baseWatchPaths = nil
	for _, basePath := range basePaths {
		paths := []string{basePath.path}
		seen := make(map[string]struct{})
		for len(paths) > 0 {
			path := paths[len(paths)-1]
			paths = paths[:len(paths)-1]
			if path == "" {
				continue
			}
			er.baseWatchPaths = append(er.baseWatchPaths, baseWatchPath{path: path, workspace: basePath.workspace})
			if dir, err := existingWatchDir(filepath.Dir(path)); err == nil {
				dirs = append(dirs, dir)
			}
			// Keep link and target parents observable even while a target is absent.
			for link := path; filepath.Dir(link) != link; link = filepath.Dir(link) {
				target, err := os.Readlink(link)
				if err != nil {
					continue
				}
				if _, exists := seen[link]; exists {
					continue
				}
				seen[link] = struct{}{}
				dirs = append(dirs, filepath.Dir(link))
				if !filepath.IsAbs(target) {
					target = filepath.Join(filepath.Dir(link), target)
				}
				rel, err := filepath.Rel(link, path)
				if err == nil {
					paths = append(paths, filepath.Join(target, rel))
				}
			}
		}
	}
	return dirs
}

func existingWatchDir(dir string) (string, error) {
	for {
		info, err := os.Stat(dir)
		if err == nil {
			if !info.IsDir() {
				return "", fmt.Errorf("base config parent %s is not a directory", dir)
			}
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if !os.IsNotExist(err) || parent == dir {
			return "", err
		}
		dir = parent
	}
}

func (er *entryReaderImpl) isBaseConfigEvent(event fsnotify.Event) bool {
	absolute, err := filepath.Abs(event.Name)
	if err != nil {
		return false
	}
	event.Name = absolute
	for _, basePath := range er.baseWatchPaths {
		if pathWithin(basePath.path, event.Name) {
			return true
		}
		if !basePath.workspace || !pathWithin(event.Name, basePath.path) {
			continue
		}
		rel, err := filepath.Rel(basePath.path, event.Name)
		if err != nil {
			continue
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if workspace.ValidateName(parts[0]) == nil &&
			(len(parts) == 1 || len(parts) == 2 && parts[1] == workspace.BaseConfigFileName) {
			return true
		}
	}
	return false
}

func pathWithin(path, dir string) bool {
	if path == "" || dir == "" {
		return false
	}
	path, pathErr := filepath.Abs(path)
	dir, dirErr := filepath.Abs(dir)
	if pathErr != nil || dirErr != nil {
		return false
	}
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

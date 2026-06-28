// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sse

import "sort"

func InitialRootWatchPathsForTest(root string) ([]string, error) {
	return initialWatchPaths(root, watchScopeRootOnly)
}

func DAGRunStatusFilePathsForTest(root string) ([]string, error) {
	files, err := scanDAGRunStatusFiles(root)
	if err != nil {
		return nil, err
	}

	paths := make([]string, 0, len(files))
	for relPath := range files {
		paths = append(paths, relPath)
	}
	sort.Strings(paths)
	return paths, nil
}

// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package wiki

import (
	"path/filepath"
	"sort"
	"strings"
	"time"

	wikimodel "github.com/dagucloud/dagu/v2/internal/wiki"
)

func normalizeSortParams(sortField wikimodel.PageSortField, sortOrder wikimodel.PageSortOrder) (string, string) {
	sf := string(sortField)
	switch sf {
	case "name", "type", "mtime":
	default:
		sf = "type"
	}
	so := string(sortOrder)
	switch so {
	case "asc", "desc":
	default:
		so = "asc"
	}
	return sf, so
}

func (s *Store) buildTreeFromIndexLocked(pathPrefix, sortField, sortOrder string, excludedRoots []string) []*wikimodel.PageTreeNode {
	dirNodes := make(map[string]*wikimodel.PageTreeNode)
	dirEntries := make(map[string]pageDirIndexEntry)
	var topLevel []*wikimodel.PageTreeNode

	var ensureDirNode func(id string) *wikimodel.PageTreeNode
	ensureDirNode = func(id string) *wikimodel.PageTreeNode {
		if id == "" {
			return nil
		}
		if node, ok := dirNodes[id]; ok {
			return node
		}
		entry := dirEntries[id]
		node := &wikimodel.PageTreeNode{
			ID:       id,
			Name:     filepath.Base(filepath.FromSlash(id)),
			Type:     "directory",
			Children: []*wikimodel.PageTreeNode{},
			ModTime:  entry.ModTime,
		}
		dirNodes[id] = node
		parentID := parentPageID(id)
		if parentID == "" {
			topLevel = append(topLevel, node)
		} else {
			parent := ensureDirNode(parentID)
			parent.Children = append(parent.Children, node)
		}
		return node
	}

	dirIDs := make([]string, 0, len(s.dirs))
	for id := range s.dirs {
		if id != "" {
			dirIDs = append(dirIDs, id)
		}
	}
	sort.Strings(dirIDs)
	for _, fullID := range dirIDs {
		if wikiPagePathRootExcluded(fullID, excludedRoots) {
			continue
		}
		id, ok := relativePageID(fullID, pathPrefix)
		if !ok {
			continue
		}
		if id != "" {
			dirEntries[id] = s.dirs[fullID]
			ensureDirNode(id)
		}
	}

	pageIDs := make([]string, 0, len(s.pages))
	for id := range s.pages {
		pageIDs = append(pageIDs, id)
	}
	sort.Strings(pageIDs)
	for _, fullID := range pageIDs {
		page := s.pages[fullID]
		if wikiPagePathRootExcluded(fullID, excludedRoots) {
			continue
		}
		id, ok := relativePageID(fullID, pathPrefix)
		if !ok {
			continue
		}
		node := &wikimodel.PageTreeNode{
			ID:      id,
			Name:    filepath.Base(filepath.FromSlash(id)) + ".md",
			Title:   page.Title,
			Tags:    page.Tags,
			Type:    "file",
			ModTime: page.ModTime,
		}
		parentID := parentPageID(id)
		if parentID == "" {
			topLevel = append(topLevel, node)
			continue
		}
		parent := ensureDirNode(parentID)
		parent.Children = append(parent.Children, node)
	}

	if sortField == "mtime" {
		propagateModTime(topLevel)
	}
	sortTreeNodes(topLevel, sortField, sortOrder)
	return topLevel
}

func propagateModTime(nodes []*wikimodel.PageTreeNode) time.Time {
	var maxTime time.Time
	for _, node := range nodes {
		t := node.ModTime
		if len(node.Children) > 0 {
			childMax := propagateModTime(node.Children)
			if childMax.After(t) {
				t = childMax
			}
			node.ModTime = t
		}
		if t.After(maxTime) {
			maxTime = t
		}
	}
	return maxTime
}

func compareNodeNames(a, b *wikimodel.PageTreeNode) int {
	if cmp := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); cmp != 0 {
		return cmp
	}
	return strings.Compare(a.ID, b.ID)
}

func compareNodeModTime(a, b *wikimodel.PageTreeNode) int {
	switch {
	case a.ModTime.Before(b.ModTime):
		return -1
	case a.ModTime.After(b.ModTime):
		return 1
	default:
		return compareNodeNames(a, b)
	}
}

func reverseCompare(cmp int) int {
	switch {
	case cmp < 0:
		return 1
	case cmp > 0:
		return -1
	default:
		return 0
	}
}

func compareTreeNodes(a, b *wikimodel.PageTreeNode, sortField, sortOrder string) int {
	switch sortField {
	case "type":
		var cmp int
		switch a.Type {
		case b.Type:
			cmp = compareNodeNames(a, b)
		case "directory":
			cmp = -1
		default:
			cmp = 1
		}
		if sortOrder == "desc" {
			return reverseCompare(cmp)
		}
		return cmp
	case "mtime":
		if a.Type != b.Type {
			if a.Type == "directory" {
				return -1
			}
			return 1
		}
		if a.Type == "directory" {
			return compareNodeNames(a, b)
		}
		cmp := compareNodeModTime(a, b)
		if sortOrder == "desc" {
			return reverseCompare(cmp)
		}
		return cmp
	default:
		cmp := compareNodeNames(a, b)
		if sortOrder == "desc" {
			return reverseCompare(cmp)
		}
		return cmp
	}
}

func sortTreeNodes(nodes []*wikimodel.PageTreeNode, sortField, sortOrder string) {
	sort.Slice(nodes, func(i, j int) bool {
		return compareTreeNodes(nodes[i], nodes[j], sortField, sortOrder) < 0
	})
	for _, node := range nodes {
		if len(node.Children) > 0 {
			sortTreeNodes(node.Children, sortField, sortOrder)
		}
	}
}

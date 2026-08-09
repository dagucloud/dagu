// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"net/url"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/wiki"
)

func toWikiPageResponse(page *wiki.Page) api.WikiPageResponse {
	resp := api.WikiPageResponse{
		Id:          page.ID,
		Title:       page.Title,
		Description: page.Description,
		Tags:        wikiPageTagsValue(page.Tags),
		Content:     page.Content,
	}
	if t, err := time.Parse(time.RFC3339, page.CreatedAt); err == nil {
		resp.CreatedAt = &t
	}
	if t, err := time.Parse(time.RFC3339, page.UpdatedAt); err == nil {
		resp.UpdatedAt = &t
	}
	return resp
}

func toWikiPageMetadataResponse(m wiki.PageMetadata) api.WikiPageMetadataResponse {
	resp := api.WikiPageMetadataResponse{
		Id:          m.ID,
		Title:       m.Title,
		Description: m.Description,
		Tags:        wikiPageTagsValue(m.Tags),
	}
	if !m.ModTime.IsZero() {
		t := m.ModTime
		resp.ModifiedAt = &t
	}
	return resp
}

// wikiPageTagsValue converts page tags to the optional response field, omitting
// the field entirely for untagged Wiki pages.
func wikiPageTagsValue(tags []string) *[]string {
	if len(tags) == 0 {
		return nil
	}
	return &tags
}

func toWikiPageTreeResponseWithWorkspace(
	node *wiki.PageTreeNode,
	workspaceName string,
	visibility wikiWorkspaceVisibility,
) api.WikiPageTreeNodeResponse {
	resp := api.WikiPageTreeNodeResponse{
		Id:        node.ID,
		Name:      node.Name,
		Title:     ptrOf(node.Title),
		Tags:      wikiPageTagsValue(node.Tags),
		Type:      api.WikiPageTreeNodeResponseType(node.Type),
		Workspace: wikiWorkspaceValue(workspaceName, node.ID, visibility, node.Type == "directory"),
	}
	if !node.ModTime.IsZero() {
		t := node.ModTime
		resp.ModifiedAt = &t
	}
	if len(node.Children) > 0 {
		children := make([]api.WikiPageTreeNodeResponse, 0, len(node.Children))
		for _, child := range node.Children {
			children = append(children, toWikiPageTreeResponseWithWorkspace(child, workspaceName, visibility))
		}
		resp.Children = &children
	}
	return resp
}

func wikiSortParams(sort *api.ListWikiPagesParamsSort, order *api.ListWikiPagesParamsOrder) (wiki.PageSortField, wiki.PageSortOrder) {
	s := wiki.PageSortFieldType
	if sort != nil {
		s = wiki.PageSortField(*sort)
	}
	o := wiki.PageSortOrderAsc
	if order != nil {
		o = wiki.PageSortOrder(*order)
	}
	return s, o
}

func wikiSortParamsFromQuery(params url.Values) (wiki.PageSortField, wiki.PageSortOrder) {
	s := wiki.PageSortField(params.Get("sort"))
	switch s {
	case wiki.PageSortFieldName, wiki.PageSortFieldType, wiki.PageSortFieldMTime:
	default:
		s = wiki.PageSortFieldType
	}
	o := wiki.PageSortOrder(params.Get("order"))
	switch o {
	case wiki.PageSortOrderAsc, wiki.PageSortOrderDesc:
	default:
		o = wiki.PageSortOrderAsc
	}
	return s, o
}

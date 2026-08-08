// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"net/url"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/docs"
)

func toDocResponse(doc *docs.Doc) api.DocResponse {
	resp := api.DocResponse{
		Id:          doc.ID,
		Title:       doc.Title,
		Description: doc.Description,
		Content:     doc.Content,
	}
	if t, err := time.Parse(time.RFC3339, doc.CreatedAt); err == nil {
		resp.CreatedAt = &t
	}
	if t, err := time.Parse(time.RFC3339, doc.UpdatedAt); err == nil {
		resp.UpdatedAt = &t
	}
	return resp
}

func toDocMetadataResponse(m docs.DocMetadata) api.DocMetadataResponse {
	resp := api.DocMetadataResponse{
		Id:          m.ID,
		Title:       m.Title,
		Description: m.Description,
	}
	if !m.ModTime.IsZero() {
		t := m.ModTime
		resp.ModifiedAt = &t
	}
	return resp
}

func toDocTreeResponse(node *docs.DocTreeNode) api.DocTreeNodeResponse {
	return toDocTreeResponseWithWorkspace(node, "", docWorkspaceVisibility{})
}

func toDocTreeResponseWithWorkspace(
	node *docs.DocTreeNode,
	workspaceName string,
	visibility docWorkspaceVisibility,
) api.DocTreeNodeResponse {
	resp := api.DocTreeNodeResponse{
		Id:        node.ID,
		Name:      node.Name,
		Title:     ptrOf(node.Title),
		Type:      api.DocTreeNodeResponseType(node.Type),
		Workspace: docWorkspaceValue(workspaceName, node.ID, visibility, node.Type == "directory"),
	}
	if !node.ModTime.IsZero() {
		t := node.ModTime
		resp.ModifiedAt = &t
	}
	if len(node.Children) > 0 {
		children := make([]api.DocTreeNodeResponse, 0, len(node.Children))
		for _, child := range node.Children {
			children = append(children, toDocTreeResponseWithWorkspace(child, workspaceName, visibility))
		}
		resp.Children = &children
	}
	return resp
}

func docSortParams(sort *api.ListDocsParamsSort, order *api.ListDocsParamsOrder) (docs.DocSortField, docs.DocSortOrder) {
	s := docs.DocSortFieldType
	if sort != nil {
		s = docs.DocSortField(*sort)
	}
	o := docs.DocSortOrderAsc
	if order != nil {
		o = docs.DocSortOrder(*order)
	}
	return s, o
}

func docSortParamsFromQuery(params url.Values) (docs.DocSortField, docs.DocSortOrder) {
	s := docs.DocSortField(params.Get("sort"))
	switch s {
	case docs.DocSortFieldName, docs.DocSortFieldType, docs.DocSortFieldMTime:
	default:
		s = docs.DocSortFieldType
	}
	o := docs.DocSortOrder(params.Get("order"))
	switch o {
	case docs.DocSortOrderAsc, docs.DocSortOrderDesc:
	default:
		o = docs.DocSortOrderAsc
	}
	return s, o
}

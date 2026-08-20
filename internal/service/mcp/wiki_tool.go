// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	daguapi "github.com/dagucloud/dagu/v2/api/v1"
	frontendapi "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
)

const defaultWikiWorkspace = "default"

var errWikiPagePathNotFound = errors.New("wiki page path not found")

type wikiPageNodeInfo struct {
	Type string
}

func (svc *Service) listWikiPages(ctx context.Context, workspace, query string) (map[string]any, error) {
	resp, err := svc.listWikiPagesResponse(ctx, workspace, query)
	if err != nil {
		return nil, err
	}
	return normalizeWikiPageList(resp, workspace), nil
}

func (svc *Service) listWikiPagesResponse(
	ctx context.Context,
	workspace string,
	query string,
) (daguapi.ListWikiPages200JSONResponse, error) {
	values, err := url.ParseQuery(query)
	if err != nil {
		return daguapi.ListWikiPages200JSONResponse{}, err
	}
	params := daguapi.ListWikiPagesParams{Workspace: wikiWorkspaceParam(workspace)}
	if value := values.Get("prefix"); value != "" {
		prefix := daguapi.WikiPagePrefix(value)
		params.Prefix = &prefix
	}
	if value := values.Get("page"); value != "" {
		page, err := strconv.Atoi(value)
		if err != nil {
			return daguapi.ListWikiPages200JSONResponse{}, err
		}
		params.Page = &page
	}
	if value := values.Get("perPage"); value != "" {
		perPage, err := strconv.Atoi(value)
		if err != nil {
			return daguapi.ListWikiPages200JSONResponse{}, err
		}
		params.PerPage = &perPage
	}
	if value := values.Get("flat"); value != "" {
		flat, err := strconv.ParseBool(value)
		if err != nil {
			return daguapi.ListWikiPages200JSONResponse{}, err
		}
		params.Flat = &flat
	}
	if value := values.Get("sort"); value != "" {
		sortField := daguapi.ListWikiPagesParamsSort(value)
		params.Sort = &sortField
	}
	if value := values.Get("order"); value != "" {
		order := daguapi.ListWikiPagesParamsOrder(value)
		params.Order = &order
	}

	resp, err := svc.api.ListWikiPages(ctx, daguapi.ListWikiPagesRequestObject{Params: params})
	if err != nil {
		return daguapi.ListWikiPages200JSONResponse{}, err
	}
	switch data := resp.(type) {
	case daguapi.ListWikiPages200JSONResponse:
		return data, nil
	case *daguapi.ListWikiPages200JSONResponse:
		return *data, nil
	default:
		return daguapi.ListWikiPages200JSONResponse{}, fmt.Errorf("unexpected Wiki page list response %T", resp)
	}
}

func (svc *Service) getWikiPage(ctx context.Context, workspace, path string) (daguapi.WikiPageResponse, error) {
	resp, err := svc.api.GetWikiPage(ctx, daguapi.GetWikiPageRequestObject{
		Params: daguapi.GetWikiPageParams{
			Workspace: wikiWorkspaceParam(workspace),
			Path:      path,
		},
	})
	if err != nil {
		return daguapi.WikiPageResponse{}, err
	}
	switch data := resp.(type) {
	case daguapi.GetWikiPage200JSONResponse:
		return daguapi.WikiPageResponse(data), nil
	case *daguapi.GetWikiPage200JSONResponse:
		return daguapi.WikiPageResponse(*data), nil
	default:
		return daguapi.WikiPageResponse{}, fmt.Errorf("unexpected Wiki page response %T", resp)
	}
}

func (svc *Service) searchWikiPages(
	ctx context.Context,
	workspace string,
	search string,
	prefix string,
	cursor string,
	limit int,
) (map[string]any, error) {
	params := daguapi.SearchWikiPageFeedParams{
		Workspace: wikiWorkspaceParam(workspace),
		Q:         search,
	}
	if prefix != "" {
		value := daguapi.WikiPagePrefix(prefix)
		params.Prefix = &value
	}
	if cursor != "" {
		value := daguapi.SearchCursor(cursor)
		params.Cursor = &value
	}
	if limit != 0 {
		value := daguapi.SearchLimit(limit)
		params.Limit = &value
	}

	resp, err := svc.api.SearchWikiPageFeed(ctx, daguapi.SearchWikiPageFeedRequestObject{
		Params: params,
	})
	if err != nil {
		return nil, err
	}
	var data daguapi.WikiPageSearchFeedResponse
	switch result := resp.(type) {
	case daguapi.SearchWikiPageFeed200JSONResponse:
		data = daguapi.WikiPageSearchFeedResponse(result)
	case *daguapi.SearchWikiPageFeed200JSONResponse:
		data = daguapi.WikiPageSearchFeedResponse(*result)
	default:
		return nil, fmt.Errorf("unexpected Wiki page search response %T", resp)
	}

	items := make([]map[string]any, 0, len(data.Results))
	for _, item := range data.Results {
		itemWorkspace, path := addressableWikiPagePath(workspace, item.Workspace, item.Id)
		entry := map[string]any{
			"id":             item.Id,
			"title":          item.Title,
			"description":    item.Description,
			"matches":        item.Matches,
			"hasMoreMatches": item.HasMoreMatches,
			"uri":            wikiPageURI(itemWorkspace, path),
		}
		if item.Tags != nil && len(*item.Tags) > 0 {
			entry["tags"] = *item.Tags
		}
		if item.ModifiedAt != nil {
			entry["modifiedAt"] = item.ModifiedAt
		}
		if item.NextMatchesCursor != nil {
			entry["nextMatchesCursor"] = *item.NextMatchesCursor
		}
		if item.Workspace != nil {
			entry["workspace"] = *item.Workspace
		}
		items = append(items, entry)
	}
	output := map[string]any{
		"results": items,
		"hasMore": data.HasMore,
	}
	if data.NextCursor != nil {
		output["nextCursor"] = *data.NextCursor
	}
	return output, nil
}

func normalizeWikiPageList(resp daguapi.ListWikiPages200JSONResponse, selectedWorkspace string) map[string]any {
	output := map[string]any{"pagination": resp.Pagination}
	if resp.Items != nil {
		items := make([]map[string]any, 0, len(*resp.Items))
		for _, item := range *resp.Items {
			workspace, path := addressableWikiPagePath(selectedWorkspace, item.Workspace, item.Id)
			entry := map[string]any{
				"id":          item.Id,
				"title":       item.Title,
				"description": item.Description,
				"uri":         wikiPageURI(workspace, path),
			}
			if item.Tags != nil && len(*item.Tags) > 0 {
				entry["tags"] = *item.Tags
			}
			if item.ModifiedAt != nil {
				entry["modifiedAt"] = item.ModifiedAt
			}
			if item.Workspace != nil {
				entry["workspace"] = *item.Workspace
			}
			items = append(items, entry)
		}
		output["items"] = items
	}
	if resp.Tree != nil {
		tree := make([]map[string]any, 0, len(*resp.Tree))
		for _, node := range *resp.Tree {
			tree = append(tree, normalizeWikiPageTreeNode(node, selectedWorkspace))
		}
		output["tree"] = tree
	}
	return output
}

func normalizeWikiPageTreeNode(node daguapi.WikiPageTreeNodeResponse, selectedWorkspace string) map[string]any {
	entry := map[string]any{
		"id":   node.Id,
		"name": node.Name,
		"type": node.Type,
	}
	if node.Title != nil {
		entry["title"] = *node.Title
	}
	if node.Tags != nil && len(*node.Tags) > 0 {
		entry["tags"] = *node.Tags
	}
	if node.ModifiedAt != nil {
		entry["modifiedAt"] = node.ModifiedAt
	}
	if node.Workspace != nil {
		entry["workspace"] = *node.Workspace
	}
	if string(node.Type) == "file" {
		workspace, path := addressableWikiPagePath(selectedWorkspace, node.Workspace, node.Id)
		entry["uri"] = wikiPageURI(workspace, path)
	}
	if node.Children != nil {
		children := make([]map[string]any, 0, len(*node.Children))
		for _, child := range *node.Children {
			children = append(children, normalizeWikiPageTreeNode(child, selectedWorkspace))
		}
		entry["children"] = children
	}
	return entry
}

func addressableWikiPagePath(selectedWorkspace string, reportedWorkspace *string, path string) (string, string) {
	workspace := selectedWorkspace
	if workspace == "" || workspace == "all" {
		workspace = defaultWikiWorkspace
		if reportedWorkspace != nil && *reportedWorkspace != "" {
			workspace = *reportedWorkspace
			path = strings.TrimPrefix(path, workspace+"/")
		}
	}
	return workspace, path
}

func normalizeWikiPage(page daguapi.WikiPageResponse, workspace string) map[string]any {
	output := map[string]any{
		"id":          page.Id,
		"title":       page.Title,
		"description": page.Description,
		"content":     page.Content,
		"mimeType":    resourceMIMEText,
		"uri":         wikiPageURI(workspace, page.Id),
	}
	if page.Tags != nil && len(*page.Tags) > 0 {
		output["tags"] = *page.Tags
	}
	if page.CreatedAt != nil {
		output["createdAt"] = page.CreatedAt
	}
	if page.UpdatedAt != nil {
		output["updatedAt"] = page.UpdatedAt
	}
	if page.Workspace != nil {
		output["workspace"] = *page.Workspace
	}
	return output
}

func inspectWikiPagePath(nodes map[string]wikiPageNodeInfo, path string) (wikiPageNodeInfo, error) {
	if node, ok := nodes[path]; ok {
		return node, nil
	}
	return wikiPageNodeInfo{}, wikiPagePathNotFoundError()
}

func (svc *Service) wikiPageNodes(ctx context.Context, workspace string) (map[string]wikiPageNodeInfo, error) {
	const perPage = 100
	nodes := make(map[string]wikiPageNodeInfo)
	for page := 1; ; page++ {
		resp, err := svc.listWikiPagesResponse(ctx, workspace, fmt.Sprintf("page=%d&perPage=%d&flat=false", page, perPage))
		if err != nil {
			return nil, err
		}
		if resp.Tree != nil {
			indexWikiPageNodes(nodes, *resp.Tree)
		}
		if page >= resp.Pagination.TotalPages {
			break
		}
	}
	return nodes, nil
}

func wikiPagePathNotFoundError() error {
	apiErr := &frontendapi.Error{
		Code:       daguapi.ErrorCodeNotFound,
		Message:    "Wiki page not found",
		HTTPStatus: http.StatusNotFound,
	}
	return fmt.Errorf("%w: %w", errWikiPagePathNotFound, apiErr)
}

func ensureWikiPagePathAvailable(nodes map[string]wikiPageNodeInfo, path string) error {
	if _, ok := nodes[path]; ok {
		return wikiPagePathConflict("Wiki destination already exists")
	}

	for parent := parentWikiPagePath(path); parent != ""; parent = parentWikiPagePath(parent) {
		if node, ok := nodes[parent]; ok && node.Type == "file" {
			return wikiPagePathConflict("Wiki destination has a page ancestor")
		}
	}
	return nil
}

func parentWikiPagePath(path string) string {
	index := strings.LastIndexByte(path, '/')
	if index < 0 {
		return ""
	}
	return path[:index]
}

func wikiPagePathConflict(message string) error {
	return &frontendapi.Error{
		Code:       daguapi.ErrorCodeConflict,
		Message:    message,
		HTTPStatus: http.StatusConflict,
	}
}

func indexWikiPageNodes(index map[string]wikiPageNodeInfo, nodes []daguapi.WikiPageTreeNodeResponse) {
	for _, node := range nodes {
		index[node.Id] = wikiPageNodeInfo{Type: string(node.Type)}
		if node.Children != nil {
			indexWikiPageNodes(index, *node.Children)
		}
	}
}

func (svc *Service) createWikiPage(ctx context.Context, workspace, path, content string) error {
	resp, err := svc.api.CreateWikiPage(ctx, daguapi.CreateWikiPageRequestObject{
		Params: daguapi.CreateWikiPageParams{Workspace: wikiWorkspaceParam(workspace)},
		Body: &daguapi.CreateWikiPageJSONRequestBody{
			Id:      path,
			Content: content,
		},
	})
	if err != nil {
		return err
	}
	switch resp.(type) {
	case daguapi.CreateWikiPage201JSONResponse, *daguapi.CreateWikiPage201JSONResponse:
		return nil
	default:
		return fmt.Errorf("unexpected create Wiki page response %T", resp)
	}
}

func (svc *Service) updateWikiPage(ctx context.Context, workspace, path, content string) error {
	resp, err := svc.api.UpdateWikiPage(ctx, daguapi.UpdateWikiPageRequestObject{
		Params: daguapi.UpdateWikiPageParams{Workspace: wikiWorkspaceParam(workspace), Path: path},
		Body:   &daguapi.UpdateWikiPageJSONRequestBody{Content: content},
	})
	if err != nil {
		return err
	}
	switch resp.(type) {
	case daguapi.UpdateWikiPage200JSONResponse, *daguapi.UpdateWikiPage200JSONResponse:
		return nil
	default:
		return fmt.Errorf("unexpected update Wiki page response %T", resp)
	}
}

func (svc *Service) renameWikiPage(ctx context.Context, workspace, path, newPath string) error {
	resp, err := svc.api.RenameWikiPage(ctx, daguapi.RenameWikiPageRequestObject{
		Params: daguapi.RenameWikiPageParams{Workspace: wikiWorkspaceParam(workspace), Path: path},
		Body:   &daguapi.RenameWikiPageJSONRequestBody{NewPath: newPath},
	})
	if err != nil {
		return err
	}
	switch resp.(type) {
	case daguapi.RenameWikiPage200JSONResponse, *daguapi.RenameWikiPage200JSONResponse:
		return nil
	default:
		return fmt.Errorf("unexpected rename Wiki page response %T", resp)
	}
}

func (svc *Service) deleteWikiPage(ctx context.Context, workspace, path string) error {
	resp, err := svc.api.DeleteWikiPage(ctx, daguapi.DeleteWikiPageRequestObject{
		Params: daguapi.DeleteWikiPageParams{Workspace: wikiWorkspaceParam(workspace), Path: path},
	})
	if err != nil {
		return err
	}
	switch resp.(type) {
	case daguapi.DeleteWikiPage204Response, *daguapi.DeleteWikiPage204Response:
		return nil
	default:
		return fmt.Errorf("unexpected delete Wiki page response %T", resp)
	}
}

func wikiWorkspaceParam(workspace string) *daguapi.Workspace {
	if workspace == "" {
		return nil
	}
	value := daguapi.Workspace(workspace)
	return &value
}

func wikiCollectionURI(workspace string) string {
	base := readResourceWikiCollectionURI
	if workspace == "" || workspace == "all" {
		return base
	}
	return base + "/" + pathEscape(workspace)
}

func wikiPageURI(workspace, path string) string {
	return wikiCollectionURI(workspace) + "/" + pathEscape(path)
}

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

const defaultDocWorkspace = "default"

var errDocPathNotFound = errors.New("document path not found")

type docNodeInfo struct {
	Type string
}

func (svc *Service) listDocs(ctx context.Context, workspace, query string) (map[string]any, error) {
	resp, err := svc.listDocsResponse(ctx, workspace, query)
	if err != nil {
		return nil, err
	}
	return normalizeDocList(resp, workspace), nil
}

func (svc *Service) listDocsResponse(
	ctx context.Context,
	workspace string,
	query string,
) (daguapi.ListDocs200JSONResponse, error) {
	values, err := url.ParseQuery(query)
	if err != nil {
		return daguapi.ListDocs200JSONResponse{}, err
	}
	params := daguapi.ListDocsParams{Workspace: docWorkspaceParam(workspace)}
	if value := values.Get("page"); value != "" {
		page, err := strconv.Atoi(value)
		if err != nil {
			return daguapi.ListDocs200JSONResponse{}, err
		}
		params.Page = &page
	}
	if value := values.Get("perPage"); value != "" {
		perPage, err := strconv.Atoi(value)
		if err != nil {
			return daguapi.ListDocs200JSONResponse{}, err
		}
		params.PerPage = &perPage
	}
	if value := values.Get("flat"); value != "" {
		flat, err := strconv.ParseBool(value)
		if err != nil {
			return daguapi.ListDocs200JSONResponse{}, err
		}
		params.Flat = &flat
	}
	if value := values.Get("sort"); value != "" {
		sortField := daguapi.ListDocsParamsSort(value)
		params.Sort = &sortField
	}
	if value := values.Get("order"); value != "" {
		order := daguapi.ListDocsParamsOrder(value)
		params.Order = &order
	}

	resp, err := svc.api.ListDocs(ctx, daguapi.ListDocsRequestObject{Params: params})
	if err != nil {
		return daguapi.ListDocs200JSONResponse{}, err
	}
	switch data := resp.(type) {
	case daguapi.ListDocs200JSONResponse:
		return data, nil
	case *daguapi.ListDocs200JSONResponse:
		return *data, nil
	default:
		return daguapi.ListDocs200JSONResponse{}, fmt.Errorf("unexpected list docs response %T", resp)
	}
}

func (svc *Service) getDoc(ctx context.Context, workspace, path string) (daguapi.DocResponse, error) {
	resp, err := svc.api.GetDoc(ctx, daguapi.GetDocRequestObject{
		Params: daguapi.GetDocParams{
			Workspace: docWorkspaceParam(workspace),
			Path:      path,
		},
	})
	if err != nil {
		return daguapi.DocResponse{}, err
	}
	switch data := resp.(type) {
	case daguapi.GetDoc200JSONResponse:
		return daguapi.DocResponse(data), nil
	case *daguapi.GetDoc200JSONResponse:
		return daguapi.DocResponse(*data), nil
	default:
		return daguapi.DocResponse{}, fmt.Errorf("unexpected get doc response %T", resp)
	}
}

func (svc *Service) searchDocs(ctx context.Context, workspace, search string) (map[string]any, error) {
	resp, err := svc.api.SearchDocs(ctx, daguapi.SearchDocsRequestObject{
		Params: daguapi.SearchDocsParams{
			Workspace: docWorkspaceParam(workspace),
			Q:         search,
		},
	})
	if err != nil {
		return nil, err
	}
	var data daguapi.DocSearchResponse
	switch result := resp.(type) {
	case daguapi.SearchDocs200JSONResponse:
		data = daguapi.DocSearchResponse(result)
	case *daguapi.SearchDocs200JSONResponse:
		data = daguapi.DocSearchResponse(*result)
	default:
		return nil, fmt.Errorf("unexpected search docs response %T", resp)
	}

	items := make([]map[string]any, 0, len(data.Results))
	for _, item := range data.Results {
		itemWorkspace, path := addressableDocPath(workspace, item.Workspace, item.Id)
		entry := map[string]any{
			"id":          item.Id,
			"title":       item.Title,
			"description": item.Description,
			"matches":     item.Matches,
			"uri":         docURI(itemWorkspace, path),
		}
		if item.Workspace != nil {
			entry["workspace"] = *item.Workspace
		}
		items = append(items, entry)
	}
	return map[string]any{"results": items}, nil
}

func normalizeDocList(resp daguapi.ListDocs200JSONResponse, selectedWorkspace string) map[string]any {
	output := map[string]any{"pagination": resp.Pagination}
	if resp.Items != nil {
		items := make([]map[string]any, 0, len(*resp.Items))
		for _, item := range *resp.Items {
			workspace, path := addressableDocPath(selectedWorkspace, item.Workspace, item.Id)
			entry := map[string]any{
				"id":          item.Id,
				"title":       item.Title,
				"description": item.Description,
				"uri":         docURI(workspace, path),
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
			tree = append(tree, normalizeDocTreeNode(node, selectedWorkspace))
		}
		output["tree"] = tree
	}
	return output
}

func normalizeDocTreeNode(node daguapi.DocTreeNodeResponse, selectedWorkspace string) map[string]any {
	entry := map[string]any{
		"id":   node.Id,
		"name": node.Name,
		"type": node.Type,
	}
	if node.Title != nil {
		entry["title"] = *node.Title
	}
	if node.ModifiedAt != nil {
		entry["modifiedAt"] = node.ModifiedAt
	}
	if node.Workspace != nil {
		entry["workspace"] = *node.Workspace
	}
	if string(node.Type) == "file" {
		workspace, path := addressableDocPath(selectedWorkspace, node.Workspace, node.Id)
		entry["uri"] = docURI(workspace, path)
	}
	if node.Children != nil {
		children := make([]map[string]any, 0, len(*node.Children))
		for _, child := range *node.Children {
			children = append(children, normalizeDocTreeNode(child, selectedWorkspace))
		}
		entry["children"] = children
	}
	return entry
}

func addressableDocPath(selectedWorkspace string, reportedWorkspace *string, path string) (string, string) {
	workspace := selectedWorkspace
	if workspace == "" || workspace == "all" {
		workspace = defaultDocWorkspace
		if reportedWorkspace != nil && *reportedWorkspace != "" {
			workspace = *reportedWorkspace
			path = strings.TrimPrefix(path, workspace+"/")
		}
	}
	return workspace, path
}

func normalizeDoc(doc daguapi.DocResponse, workspace string) map[string]any {
	output := map[string]any{
		"id":          doc.Id,
		"title":       doc.Title,
		"description": doc.Description,
		"content":     doc.Content,
		"mimeType":    resourceMIMEText,
		"uri":         docURI(workspace, doc.Id),
	}
	if doc.CreatedAt != nil {
		output["createdAt"] = doc.CreatedAt
	}
	if doc.UpdatedAt != nil {
		output["updatedAt"] = doc.UpdatedAt
	}
	if doc.Workspace != nil {
		output["workspace"] = *doc.Workspace
	}
	return output
}

func (svc *Service) inspectDocPath(ctx context.Context, workspace, path string) (docNodeInfo, error) {
	nodes, err := svc.docNodes(ctx, workspace)
	if err != nil {
		return docNodeInfo{}, err
	}
	if node, ok := nodes[path]; ok {
		return node, nil
	}
	return docNodeInfo{}, docPathNotFoundError()
}

func (svc *Service) docNodes(ctx context.Context, workspace string) (map[string]docNodeInfo, error) {
	const perPage = 100
	nodes := make(map[string]docNodeInfo)
	for page := 1; ; page++ {
		resp, err := svc.listDocsResponse(ctx, workspace, fmt.Sprintf("page=%d&perPage=%d", page, perPage))
		if err != nil {
			return nil, err
		}
		if resp.Tree != nil {
			indexDocNodes(nodes, *resp.Tree)
		}
		if page >= resp.Pagination.TotalPages {
			break
		}
	}
	return nodes, nil
}

func docPathNotFoundError() error {
	return fmt.Errorf("%w: %w", errDocPathNotFound, &frontendapi.Error{
		Code:       daguapi.ErrorCodeNotFound,
		Message:    "Document not found",
		HTTPStatus: http.StatusNotFound,
	})
}

func (svc *Service) ensureDocPathAvailable(ctx context.Context, workspace, path string) error {
	nodes, err := svc.docNodes(ctx, workspace)
	if err != nil {
		return err
	}
	if _, ok := nodes[path]; ok {
		return docPathConflict("Document destination already exists")
	}

	for parent := parentDocPath(path); parent != ""; parent = parentDocPath(parent) {
		if node, ok := nodes[parent]; ok && node.Type == "file" {
			return docPathConflict("Document destination has a file ancestor")
		}
	}
	return nil
}

func parentDocPath(path string) string {
	index := strings.LastIndexByte(path, '/')
	if index < 0 {
		return ""
	}
	return path[:index]
}

func docPathConflict(message string) error {
	return &frontendapi.Error{
		Code:       daguapi.ErrorCodeConflict,
		Message:    message,
		HTTPStatus: http.StatusConflict,
	}
}

func indexDocNodes(index map[string]docNodeInfo, nodes []daguapi.DocTreeNodeResponse) {
	for _, node := range nodes {
		index[node.Id] = docNodeInfo{Type: string(node.Type)}
		if node.Children != nil {
			indexDocNodes(index, *node.Children)
		}
	}
}

func (svc *Service) createDoc(ctx context.Context, workspace, path, content string) error {
	resp, err := svc.api.CreateDoc(ctx, daguapi.CreateDocRequestObject{
		Params: daguapi.CreateDocParams{Workspace: docWorkspaceParam(workspace)},
		Body: &daguapi.CreateDocJSONRequestBody{
			Id:      path,
			Content: content,
		},
	})
	if err != nil {
		return err
	}
	switch resp.(type) {
	case daguapi.CreateDoc201JSONResponse, *daguapi.CreateDoc201JSONResponse:
		return nil
	default:
		return fmt.Errorf("unexpected create doc response %T", resp)
	}
}

func (svc *Service) updateDoc(ctx context.Context, workspace, path, content string) error {
	resp, err := svc.api.UpdateDoc(ctx, daguapi.UpdateDocRequestObject{
		Params: daguapi.UpdateDocParams{Workspace: docWorkspaceParam(workspace), Path: path},
		Body:   &daguapi.UpdateDocJSONRequestBody{Content: content},
	})
	if err != nil {
		return err
	}
	switch resp.(type) {
	case daguapi.UpdateDoc200JSONResponse, *daguapi.UpdateDoc200JSONResponse:
		return nil
	default:
		return fmt.Errorf("unexpected update doc response %T", resp)
	}
}

func (svc *Service) renameDoc(ctx context.Context, workspace, path, newPath string) error {
	resp, err := svc.api.RenameDoc(ctx, daguapi.RenameDocRequestObject{
		Params: daguapi.RenameDocParams{Workspace: docWorkspaceParam(workspace), Path: path},
		Body:   &daguapi.RenameDocJSONRequestBody{NewPath: newPath},
	})
	if err != nil {
		return err
	}
	switch resp.(type) {
	case daguapi.RenameDoc200JSONResponse, *daguapi.RenameDoc200JSONResponse:
		return nil
	default:
		return fmt.Errorf("unexpected rename doc response %T", resp)
	}
}

func (svc *Service) deleteDoc(ctx context.Context, workspace, path string) error {
	resp, err := svc.api.DeleteDoc(ctx, daguapi.DeleteDocRequestObject{
		Params: daguapi.DeleteDocParams{Workspace: docWorkspaceParam(workspace), Path: path},
	})
	if err != nil {
		return err
	}
	switch resp.(type) {
	case daguapi.DeleteDoc204Response, *daguapi.DeleteDoc204Response:
		return nil
	default:
		return fmt.Errorf("unexpected delete doc response %T", resp)
	}
}

func docWorkspaceParam(workspace string) *daguapi.Workspace {
	if workspace == "" {
		return nil
	}
	value := daguapi.Workspace(workspace)
	return &value
}

func docsCollectionURI(workspace string) string {
	base := "dagu://docs"
	if workspace == "" || workspace == "all" {
		return base
	}
	return base + "/" + pathEscape(workspace)
}

func docURI(workspace, path string) string {
	return docsCollectionURI(workspace) + "/" + pathEscape(path)
}

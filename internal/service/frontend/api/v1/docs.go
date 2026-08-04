// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/core/docs"
	"github.com/dagucloud/dagu/v2/internal/service/audit"
)

const (
	auditActionDocCreate = "doc_create"
	auditActionDocUpdate = "doc_update"
	auditActionDocDelete = "doc_delete"
	auditActionDocRename = "doc_rename"
)

var (
	errDocStoreNotAvailable = &Error{
		Code:       api.ErrorCodeForbidden,
		Message:    "Document management is not available",
		HTTPStatus: http.StatusForbidden,
	}

	errDocNotFound = &Error{
		Code:       api.ErrorCodeNotFound,
		Message:    "Document not found",
		HTTPStatus: http.StatusNotFound,
	}

	errDocAlreadyExists = &Error{
		Code:       api.ErrorCodeAlreadyExists,
		Message:    "Document already exists",
		HTTPStatus: http.StatusConflict,
	}

	errDocPathConflict = &Error{
		Code:       api.ErrorCodeConflict,
		Message:    "Document path conflicts with an existing file or directory",
		HTTPStatus: http.StatusConflict,
	}
)

func (a *API) requireDocManagement() error {
	if a.docStore == nil {
		return errDocStoreNotAvailable
	}
	return nil
}

// ListDocs returns documents as tree or flat list.
func (a *API) ListDocs(ctx context.Context, request api.ListDocsRequestObject) (api.ListDocsResponseObject, error) {
	if err := a.requireDocManagement(); err != nil {
		return nil, err
	}
	a.workspaceDocMu.RLock()
	defer a.workspaceDocMu.RUnlock()
	workspaceName, visibility, err := a.docReadScopeForParams(ctx, request.Params.Workspace)
	if err != nil {
		return nil, err
	}

	sortField, sortOrder := docSortParams(request.Params.Sort, request.Params.Order)

	opts := docs.ListDocsOptions{
		Page:             valueOf(request.Params.Page),
		PerPage:          valueOf(request.Params.PerPage),
		Sort:             sortField,
		Order:            sortOrder,
		PathPrefix:       workspaceName,
		ExcludePathRoots: visibility.excludedPathRoots(),
	}

	flat := valueOf(request.Params.Flat)

	if flat {
		result, err := a.docStore.ListFlat(ctx, opts)
		if err != nil {
			logger.Error(ctx, "Failed to list docs flat", tag.Error(err))
			return nil, internalError(err)
		}

		items := make([]api.DocMetadataResponse, 0, len(result.Items))
		for _, m := range result.Items {
			item := toDocMetadataResponse(m)
			item.Workspace = docWorkspaceValue(workspaceName, m.ID, visibility, false)
			items = append(items, item)
		}

		return api.ListDocs200JSONResponse{
			Items:      &items,
			Pagination: toPagination(*result),
		}, nil
	}

	result, err := a.docStore.List(ctx, opts)
	if err != nil {
		logger.Error(ctx, "Failed to list docs tree", tag.Error(err))
		return nil, internalError(err)
	}

	tree := make([]api.DocTreeNodeResponse, 0, len(result.Items))
	for _, node := range result.Items {
		tree = append(tree, toDocTreeResponseWithWorkspace(node, workspaceName, visibility))
	}

	return api.ListDocs200JSONResponse{
		Tree:       &tree,
		Pagination: toPagination(*result),
	}, nil
}

// CreateDoc creates a new document.
func (a *API) CreateDoc(ctx context.Context, request api.CreateDocRequestObject) (api.CreateDocResponseObject, error) {
	if err := a.requireDocManagement(); err != nil {
		return nil, err
	}
	a.workspaceDocMu.RLock()
	defer a.workspaceDocMu.RUnlock()
	if request.Body == nil {
		return nil, ErrInvalidRequestBody
	}
	workspaceName, err := docMutationScopeForParams(request.Params.Workspace)
	if err != nil {
		return nil, err
	}
	if err := a.requireDAGWriteForWorkspace(ctx, workspaceName); err != nil {
		return nil, err
	}

	id := request.Body.Id
	scopedID, err := a.scopedDocMutationPath(ctx, workspaceName, id)
	if err != nil {
		return nil, err
	}

	if err := a.docStore.Create(ctx, scopedID, request.Body.Content); err != nil {
		if errors.Is(err, docs.ErrDocAlreadyExists) {
			return nil, errDocAlreadyExists
		}
		if errors.Is(err, docs.ErrDocPathConflict) {
			return nil, errDocPathConflict
		}
		logger.Error(ctx, "Failed to create doc", tag.Error(err))
		return nil, internalError(err)
	}

	a.logAudit(ctx, audit.CategoryDoc, auditActionDocCreate, map[string]any{
		"doc_id":    id,
		"workspace": workspaceName,
	})
	a.notifyDocMutation()

	msg := fmt.Sprintf("Document %s created", id)
	return api.CreateDoc201JSONResponse{Message: &msg}, nil
}

// GetDoc returns a single document.
func (a *API) GetDoc(ctx context.Context, request api.GetDocRequestObject) (api.GetDocResponseObject, error) {
	if err := a.requireDocManagement(); err != nil {
		return nil, err
	}
	a.workspaceDocMu.RLock()
	defer a.workspaceDocMu.RUnlock()
	workspaceName, visibility, err := a.docPointReadScopeForParams(ctx, request.Params.Workspace)
	if err != nil {
		return nil, err
	}
	docID, err := scopedDocPath(workspaceName, request.Params.Path)
	if err != nil {
		return nil, err
	}
	doc, err := a.docStore.Get(ctx, docID)
	if err != nil {
		if errors.Is(err, docs.ErrDocNotFound) {
			return nil, errDocNotFound
		}
		return nil, internalError(err)
	}
	if workspaceName == "" && !visibility.all {
		if !visibility.visible(doc.ID) {
			return nil, errDocNotFound
		}
	}
	rawID := doc.ID
	doc.ID = visibleDocPath(workspaceName, doc.ID)
	resp := toDocResponse(doc)
	resp.Workspace = docWorkspaceValue(workspaceName, rawID, visibility, false)

	return api.GetDoc200JSONResponse(resp), nil
}

// SearchDocs searches document content.
func (a *API) SearchDocs(ctx context.Context, request api.SearchDocsRequestObject) (api.SearchDocsResponseObject, error) {
	if err := a.requireDocManagement(); err != nil {
		return nil, err
	}
	a.workspaceDocMu.RLock()
	defer a.workspaceDocMu.RUnlock()

	query, err := validateSearchQuery(request.Params.Q)
	if err != nil {
		return nil, err
	}
	workspaceName, visibility, err := a.docReadScopeForParams(ctx, request.Params.Workspace)
	if err != nil {
		return nil, err
	}

	results, err := a.docStore.Search(ctx, query)
	if err != nil {
		logger.Error(ctx, "Failed to search docs", tag.Error(err))
		return nil, internalError(err)
	}

	items := make([]api.DocSearchResultItem, 0, len(results))
	for _, r := range results {
		rawID := r.ID
		if workspaceName != "" {
			prefix := workspaceName + "/"
			if !strings.HasPrefix(r.ID, prefix) {
				continue
			}
			r.ID = strings.TrimPrefix(r.ID, prefix)
		} else if !visibility.visible(r.ID) {
			continue
		}
		item := api.DocSearchResultItem{
			Id:          r.ID,
			Title:       r.Title,
			Description: r.Description,
			Workspace:   docWorkspaceValue(workspaceName, rawID, visibility, false),
		}
		if len(r.Matches) > 0 {
			matches := make([]api.SearchMatchItem, 0, len(r.Matches))
			for _, m := range r.Matches {
				matches = append(matches, api.SearchMatchItem{
					Line:       m.Line,
					LineNumber: m.LineNumber,
					StartLine:  m.StartLine,
				})
			}
			item.Matches = &matches
		}
		items = append(items, item)
	}

	return api.SearchDocs200JSONResponse{
		Results: items,
	}, nil
}

// UpdateDoc updates document content.
func (a *API) UpdateDoc(ctx context.Context, request api.UpdateDocRequestObject) (api.UpdateDocResponseObject, error) {
	if err := a.requireDocManagement(); err != nil {
		return nil, err
	}
	a.workspaceDocMu.RLock()
	defer a.workspaceDocMu.RUnlock()
	if request.Body == nil {
		return nil, ErrInvalidRequestBody
	}
	workspaceName, err := docMutationScopeForParams(request.Params.Workspace)
	if err != nil {
		return nil, err
	}
	if err := a.requireDAGWriteForWorkspace(ctx, workspaceName); err != nil {
		return nil, err
	}
	docID, err := a.scopedDocMutationPath(ctx, workspaceName, request.Params.Path)
	if err != nil {
		return nil, err
	}
	if err := a.docStore.Update(ctx, docID, request.Body.Content); err != nil {
		if errors.Is(err, docs.ErrDocNotFound) {
			return nil, errDocNotFound
		}
		logger.Error(ctx, "Failed to update doc", tag.Error(err))
		return nil, internalError(err)
	}

	a.logAudit(ctx, audit.CategoryDoc, auditActionDocUpdate, map[string]any{
		"doc_id":    request.Params.Path,
		"workspace": workspaceName,
	})
	a.notifyDocMutation()

	msg := "Document updated"
	return api.UpdateDoc200JSONResponse{Message: &msg}, nil
}

// DeleteDoc removes a document.
func (a *API) DeleteDoc(ctx context.Context, request api.DeleteDocRequestObject) (api.DeleteDocResponseObject, error) {
	if err := a.requireDocManagement(); err != nil {
		return nil, err
	}
	a.workspaceDocMu.RLock()
	defer a.workspaceDocMu.RUnlock()
	workspaceName, err := docMutationScopeForParams(request.Params.Workspace)
	if err != nil {
		return nil, err
	}
	if err := a.requireDAGWriteForWorkspace(ctx, workspaceName); err != nil {
		return nil, err
	}
	docID, err := a.scopedDocMutationPath(ctx, workspaceName, request.Params.Path)
	if err != nil {
		return nil, err
	}

	if err := a.docStore.Delete(ctx, docID); err != nil {
		if errors.Is(err, docs.ErrDocNotFound) {
			return nil, errDocNotFound
		}
		if errors.Is(err, docs.ErrDocPathConflict) {
			return nil, errDocPathConflict
		}
		logger.Error(ctx, "Failed to delete doc", tag.Error(err))
		return nil, internalError(err)
	}

	a.logAudit(ctx, audit.CategoryDoc, auditActionDocDelete, map[string]any{
		"doc_id":    request.Params.Path,
		"workspace": workspaceName,
	})
	a.notifyDocMutation()

	return api.DeleteDoc204Response{}, nil
}

// RenameDoc renames/moves a document.
func (a *API) RenameDoc(ctx context.Context, request api.RenameDocRequestObject) (api.RenameDocResponseObject, error) {
	if err := a.requireDocManagement(); err != nil {
		return nil, err
	}
	a.workspaceDocMu.RLock()
	defer a.workspaceDocMu.RUnlock()
	if request.Body == nil {
		return nil, ErrInvalidRequestBody
	}
	workspaceName, err := docMutationScopeForParams(request.Params.Workspace)
	if err != nil {
		return nil, err
	}
	if err := a.requireDAGWriteForWorkspace(ctx, workspaceName); err != nil {
		return nil, err
	}
	oldPath, err := a.scopedDocMutationPath(ctx, workspaceName, request.Params.Path)
	if err != nil {
		return nil, err
	}
	newPath, err := a.scopedDocMutationPath(ctx, workspaceName, request.Body.NewPath)
	if err != nil {
		return nil, err
	}

	if err := a.docStore.Rename(ctx, oldPath, newPath); err != nil {
		if errors.Is(err, docs.ErrDocNotFound) {
			return nil, errDocNotFound
		}
		if errors.Is(err, docs.ErrDocAlreadyExists) {
			return nil, errDocAlreadyExists
		}
		if errors.Is(err, docs.ErrDocPathConflict) {
			return nil, errDocPathConflict
		}
		logger.Error(ctx, "Failed to rename doc", tag.Error(err))
		return nil, internalError(err)
	}

	a.logAudit(ctx, audit.CategoryDoc, auditActionDocRename, map[string]any{
		"old_path":  request.Params.Path,
		"new_path":  request.Body.NewPath,
		"workspace": workspaceName,
	})
	a.notifyDocMutation()

	msg := fmt.Sprintf("Document renamed to %s", request.Body.NewPath)
	return api.RenameDoc200JSONResponse{Message: &msg}, nil
}

// DeleteDocBatch deletes multiple documents or directories.
func (a *API) DeleteDocBatch(ctx context.Context, request api.DeleteDocBatchRequestObject) (api.DeleteDocBatchResponseObject, error) {
	if err := a.requireDocManagement(); err != nil {
		return nil, err
	}
	a.workspaceDocMu.RLock()
	defer a.workspaceDocMu.RUnlock()
	if request.Body == nil || len(request.Body.Paths) == 0 {
		return nil, &Error{
			Code:       api.ErrorCodeBadRequest,
			Message:    "paths required",
			HTTPStatus: http.StatusBadRequest,
		}
	}
	if len(request.Body.Paths) > 100 {
		return nil, &Error{
			Code:       api.ErrorCodeBadRequest,
			Message:    "max 100 paths per batch",
			HTTPStatus: http.StatusBadRequest,
		}
	}
	workspaceName, err := docMutationScopeForParams(request.Params.Workspace)
	if err != nil {
		return nil, err
	}
	if err := a.requireDAGWriteForWorkspace(ctx, workspaceName); err != nil {
		return nil, err
	}
	scopedPaths := make([]string, 0, len(request.Body.Paths))
	for _, p := range request.Body.Paths {
		scoped, err := a.scopedDocMutationPath(ctx, workspaceName, p)
		if err != nil {
			return nil, err
		}
		scopedPaths = append(scopedPaths, scoped)
	}

	deleted, failed, err := a.docStore.DeleteBatch(ctx, scopedPaths)
	if err != nil {
		logger.Error(ctx, "Failed to batch delete docs", tag.Error(err))
		return nil, internalError(err)
	}

	visibleDeleted := make([]string, 0, len(deleted))
	for _, id := range deleted {
		visibleID := visibleDocPath(workspaceName, id)
		visibleDeleted = append(visibleDeleted, visibleID)
		a.logAudit(ctx, audit.CategoryDoc, auditActionDocDelete, map[string]any{
			"doc_id":    visibleID,
			"workspace": workspaceName,
		})
	}

	failedItems := make([]api.DocDeleteBatchFailedItem, 0, len(failed))
	for _, f := range failed {
		failedItems = append(failedItems, api.DocDeleteBatchFailedItem{
			Path:  visibleDocPath(workspaceName, f.ID),
			Error: f.Error,
		})
	}
	if len(visibleDeleted) > 0 {
		a.notifyDocMutation()
	}

	msg := fmt.Sprintf("Deleted %d, failed %d", len(visibleDeleted), len(failed))
	return api.DeleteDocBatch200JSONResponse{
		Deleted: visibleDeleted,
		Failed:  failedItems,
		Message: msg,
	}, nil
}

// GetDocTreeData is the SSE data method for the doc tree.
// Identifier format: URL query string (e.g., "page=1&perPage=200")
func (a *API) GetDocTreeData(ctx context.Context, queryString string) (any, error) {
	return withDAGRunReadTimeout(ctx, dagRunReadRequestInfo{
		endpoint: "/docs/tree",
	}, func(readCtx context.Context) (any, error) {
		a.workspaceDocMu.RLock()
		defer a.workspaceDocMu.RUnlock()
		if a.docStore == nil {
			return nil, errDocStoreNotAvailable
		}

		params, err := url.ParseQuery(queryString)
		if err != nil {
			return nil, fmt.Errorf("invalid doc tree query: %w", err)
		}

		page := parseIntParam(params.Get("page"), 1)
		perPage := min(parseIntParam(params.Get("perPage"), 200), 200)
		workspaceParam := workspaceParamFromValues(params)
		workspaceName, visibility, err := a.docReadScopeForParams(readCtx, workspaceParam)
		if err != nil {
			return nil, err
		}

		sortField, sortOrder := docSortParamsFromQuery(params)

		result, err := a.docStore.List(readCtx, docs.ListDocsOptions{
			Page:             page,
			PerPage:          perPage,
			Sort:             sortField,
			Order:            sortOrder,
			PathPrefix:       workspaceName,
			ExcludePathRoots: visibility.excludedPathRoots(),
		})
		if err != nil {
			return nil, err
		}

		tree := make([]api.DocTreeNodeResponse, 0, len(result.Items))
		for _, node := range result.Items {
			tree = append(tree, toDocTreeResponseWithWorkspace(node, workspaceName, visibility))
		}

		return api.ListDocs200JSONResponse{
			Tree:       &tree,
			Pagination: toPagination(*result),
		}, nil
	})
}

// GetDocContentData is the SSE data method for doc content.
func (a *API) GetDocContentData(ctx context.Context, docID string) (any, error) {
	return withDAGRunReadTimeout(ctx, dagRunReadRequestInfo{
		endpoint: "/docs/{docID}",
	}, func(readCtx context.Context) (any, error) {
		a.workspaceDocMu.RLock()
		defer a.workspaceDocMu.RUnlock()
		if a.docStore == nil {
			return nil, errDocStoreNotAvailable
		}
		path, queryString, hasQuery := strings.Cut(docID, "?")
		var (
			workspaceName string
			visibility    docWorkspaceVisibility
			err           error
			params        url.Values
		)
		if hasQuery {
			params, err = url.ParseQuery(queryString)
			if err != nil {
				return nil, err
			}
			workspaceParam := workspaceParamFromValues(params)
			workspaceName, visibility, err = a.docPointReadScopeForParams(readCtx, workspaceParam)
			if err != nil {
				return nil, err
			}
		} else {
			workspaceName, visibility, err = a.docPointReadScopeForParams(readCtx, nil)
			if err != nil {
				return nil, err
			}
		}
		scopedID, err := scopedDocPath(workspaceName, path)
		if err != nil {
			return nil, err
		}
		doc, err := a.docStore.Get(readCtx, scopedID)
		if err != nil {
			return nil, err
		}
		if workspaceName == "" && !visibility.all {
			if !visibility.visible(doc.ID) {
				return nil, errDocNotFound
			}
		}
		rawID := doc.ID
		doc.ID = visibleDocPath(workspaceName, doc.ID)
		resp := toDocResponse(doc)
		resp.Workspace = docWorkspaceValue(workspaceName, rawID, visibility, false)
		return resp, nil
	})
}

// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/wiki"
)

const (
	auditActionWikiPageCreate           = "doc_create"
	auditActionWikiPageUpdate           = "doc_update"
	auditActionWikiPageDelete           = "doc_delete"
	auditActionWikiPageRename           = "doc_rename"
	auditActionWikiPageAttachmentUpload = "doc_attachment_upload"
)

var (
	errWikiStoreNotAvailable = &Error{
		Code:       api.ErrorCodeForbidden,
		Message:    "Wiki page management is not available",
		HTTPStatus: http.StatusForbidden,
	}

	errWikiPageNotFound = &Error{
		Code:       api.ErrorCodeNotFound,
		Message:    "Wiki page not found",
		HTTPStatus: http.StatusNotFound,
	}

	errWikiPageAlreadyExists = &Error{
		Code:       api.ErrorCodeAlreadyExists,
		Message:    "Wiki page already exists",
		HTTPStatus: http.StatusConflict,
	}

	errWikiPagePathConflict = &Error{
		Code:       api.ErrorCodeConflict,
		Message:    "Wiki page path conflicts with an existing file or directory",
		HTTPStatus: http.StatusConflict,
	}

	errWikiPageRevisionNotFound = &Error{
		Code:       api.ErrorCodeNotFound,
		Message:    "Wiki page revision not found",
		HTTPStatus: http.StatusNotFound,
	}

	errWikiPageAttachmentNotFound = &Error{
		Code:       api.ErrorCodeNotFound,
		Message:    "Wiki page attachment not found",
		HTTPStatus: http.StatusNotFound,
	}
)

func (a *API) requireWikiManagement() error {
	if a.wikiStore == nil {
		return errWikiStoreNotAvailable
	}
	return nil
}

// ListWikiPages returns Wiki pages as tree or flat list.
func (a *API) ListWikiPages(ctx context.Context, request api.ListWikiPagesRequestObject) (api.ListWikiPagesResponseObject, error) {
	if err := a.requireWikiManagement(); err != nil {
		return nil, err
	}
	a.workspaceWikiMu.RLock()
	defer a.workspaceWikiMu.RUnlock()
	workspaceName, visibility, err := a.wikiReadScopeForParams(ctx, request.Params.Workspace)
	if err != nil {
		return nil, err
	}
	visiblePrefix := string(valueOf(request.Params.Prefix))
	pathPrefix, err := scopedWikiPageListPrefix(workspaceName, visiblePrefix)
	if err != nil {
		return nil, err
	}

	sortField, sortOrder := wikiSortParams(request.Params.Sort, request.Params.Order)

	opts := wiki.ListPagesOptions{
		Page:             valueOf(request.Params.Page),
		PerPage:          valueOf(request.Params.PerPage),
		Sort:             sortField,
		Order:            sortOrder,
		PathPrefix:       pathPrefix,
		Tags:             valueOf(request.Params.Tags),
		ExcludePathRoots: visibility.excludedPathRoots(),
	}

	flat := valueOf(request.Params.Flat)

	if flat {
		result, err := a.wikiStore.ListFlat(ctx, opts)
		if err != nil {
			logger.Error(ctx, "Failed to list Wiki pages flat", tag.Error(err))
			return nil, internalError(err)
		}

		items := make([]api.WikiPageMetadataResponse, 0, len(result.Items))
		for _, m := range result.Items {
			m.ID = visibleWikiPageListPath(visiblePrefix, m.ID)
			item := toWikiPageMetadataResponse(m)
			item.Workspace = wikiWorkspaceValue(workspaceName, m.ID, visibility, false)
			items = append(items, item)
		}

		return api.ListWikiPages200JSONResponse{
			Items:      &items,
			Pagination: toPagination(*result),
		}, nil
	}

	result, err := a.wikiStore.List(ctx, opts)
	if err != nil {
		logger.Error(ctx, "Failed to list Wiki pages tree", tag.Error(err))
		return nil, internalError(err)
	}

	tree := make([]api.WikiPageTreeNodeResponse, 0, len(result.Items))
	for _, node := range result.Items {
		restoreWikiTreePrefix(node, visiblePrefix)
		tree = append(tree, toWikiPageTreeResponseWithWorkspace(node, workspaceName, visibility))
	}

	return api.ListWikiPages200JSONResponse{
		Tree:       &tree,
		Pagination: toPagination(*result),
	}, nil
}

// CreateWikiPage creates a new Wiki page.
func (a *API) CreateWikiPage(ctx context.Context, request api.CreateWikiPageRequestObject) (api.CreateWikiPageResponseObject, error) {
	if err := a.requireWikiManagement(); err != nil {
		return nil, err
	}
	a.workspaceWikiMu.Lock()
	defer a.workspaceWikiMu.Unlock()
	if request.Body == nil {
		return nil, ErrInvalidRequestBody
	}
	workspaceName, err := wikiMutationScopeForParams(request.Params.Workspace)
	if err != nil {
		return nil, err
	}
	if err := a.requireDAGWriteForWorkspace(ctx, workspaceName); err != nil {
		return nil, err
	}

	id := request.Body.Id
	scopedID, err := a.scopedWikiPageMutationPath(ctx, workspaceName, id)
	if err != nil {
		return nil, err
	}

	if err := a.wikiStore.Create(ctx, scopedID, request.Body.Content); err != nil {
		if errors.Is(err, wiki.ErrPageAlreadyExists) {
			return nil, errWikiPageAlreadyExists
		}
		if errors.Is(err, wiki.ErrPagePathConflict) {
			return nil, errWikiPagePathConflict
		}
		logger.Error(ctx, "Failed to create page", tag.Error(err))
		return nil, internalError(err)
	}

	a.logAudit(ctx, audit.CategoryWiki, auditActionWikiPageCreate, map[string]any{
		"doc_id":    id,
		"workspace": workspaceName,
	})
	a.notifyWikiMutation()

	msg := fmt.Sprintf("Wiki page %s created", id)
	return api.CreateWikiPage201JSONResponse{Message: &msg}, nil
}

// GetWikiPage returns a single Wiki page.
func (a *API) GetWikiPage(ctx context.Context, request api.GetWikiPageRequestObject) (api.GetWikiPageResponseObject, error) {
	if err := a.requireWikiManagement(); err != nil {
		return nil, err
	}
	a.workspaceWikiMu.RLock()
	defer a.workspaceWikiMu.RUnlock()
	workspaceName, visibility, err := a.wikiPointReadScopeForParams(ctx, request.Params.Workspace)
	if err != nil {
		return nil, err
	}
	pageID, err := scopedWikiPagePath(workspaceName, request.Params.Path)
	if err != nil {
		return nil, err
	}
	page, err := a.wikiStore.Get(ctx, pageID)
	if err != nil {
		if errors.Is(err, wiki.ErrPageNotFound) {
			return nil, errWikiPageNotFound
		}
		return nil, internalError(err)
	}
	if workspaceName == "" && !visibility.all {
		if !visibility.visible(page.ID) {
			return nil, errWikiPageNotFound
		}
	}
	rawID := page.ID
	page.ID = visibleWikiPagePath(workspaceName, page.ID)
	resp := toWikiPageResponse(page)
	resp.Workspace = wikiWorkspaceValue(workspaceName, rawID, visibility, false)

	return api.GetWikiPage200JSONResponse(resp), nil
}

// wikiPointReadScopedID resolves and authorizes a page path for point reads,
// returning the stored ID after verifying the Wiki page exists and is visible.
func (a *API) wikiPointReadScopedID(ctx context.Context, workspaceParam *api.Workspace, path string) (string, error) {
	workspaceName, visibility, err := a.wikiPointReadScopeForParams(ctx, workspaceParam)
	if err != nil {
		return "", err
	}
	pageID, err := scopedWikiPagePath(workspaceName, path)
	if err != nil {
		return "", err
	}
	page, err := a.wikiStore.Get(ctx, pageID)
	if err != nil {
		if errors.Is(err, wiki.ErrPageNotFound) {
			return "", errWikiPageNotFound
		}
		return "", internalError(err)
	}
	if workspaceName == "" && !visibility.all && !visibility.visible(page.ID) {
		return "", errWikiPageNotFound
	}
	return pageID, nil
}

// ListWikiPageRevisions returns stored prior versions of a Wiki page.
func (a *API) ListWikiPageRevisions(ctx context.Context, request api.ListWikiPageRevisionsRequestObject) (api.ListWikiPageRevisionsResponseObject, error) {
	if err := a.requireWikiManagement(); err != nil {
		return nil, err
	}
	a.workspaceWikiMu.RLock()
	defer a.workspaceWikiMu.RUnlock()
	pageID, err := a.wikiPointReadScopedID(ctx, request.Params.Workspace, request.Params.Path)
	if err != nil {
		return nil, err
	}
	revisions, err := a.wikiStore.ListRevisions(ctx, pageID)
	if err != nil {
		logger.Error(ctx, "Failed to list page revisions", tag.Error(err))
		return nil, internalError(err)
	}
	items := make([]api.WikiPageRevisionResponse, 0, len(revisions))
	for _, revision := range revisions {
		items = append(items, api.WikiPageRevisionResponse{
			Rev:     revision.Rev,
			SavedAt: revision.SavedAt,
			Size:    revision.Size,
		})
	}
	return api.ListWikiPageRevisions200JSONResponse{Revisions: items}, nil
}

// GetWikiPageRevision returns one stored revision including its content.
func (a *API) GetWikiPageRevision(ctx context.Context, request api.GetWikiPageRevisionRequestObject) (api.GetWikiPageRevisionResponseObject, error) {
	if err := a.requireWikiManagement(); err != nil {
		return nil, err
	}
	a.workspaceWikiMu.RLock()
	defer a.workspaceWikiMu.RUnlock()
	pageID, err := a.wikiPointReadScopedID(ctx, request.Params.Workspace, request.Params.Path)
	if err != nil {
		return nil, err
	}
	revision, err := a.wikiStore.GetRevision(ctx, pageID, request.Params.Rev)
	if err != nil {
		if errors.Is(err, wiki.ErrPageRevisionNotFound) {
			return nil, errWikiPageRevisionNotFound
		}
		logger.Error(ctx, "Failed to get page revision", tag.Error(err))
		return nil, internalError(err)
	}
	return api.GetWikiPageRevision200JSONResponse(api.WikiPageRevisionResponse{
		Rev:     revision.Rev,
		SavedAt: revision.SavedAt,
		Size:    revision.Size,
		Content: ptrOf(revision.Content),
	}), nil
}

// maxWikiPageAttachmentSize caps a single attachment upload.
const maxWikiPageAttachmentSize = 10 << 20 // 10 MiB

// UploadWikiPageAttachment stores a binary attachment for an existing Wiki page.
func (a *API) UploadWikiPageAttachment(ctx context.Context, request api.UploadWikiPageAttachmentRequestObject) (api.UploadWikiPageAttachmentResponseObject, error) {
	if err := a.requireWikiManagement(); err != nil {
		return nil, err
	}
	a.workspaceWikiMu.Lock()
	defer a.workspaceWikiMu.Unlock()
	if request.Body == nil {
		return nil, ErrInvalidRequestBody
	}
	workspaceName, err := wikiMutationScopeForParams(request.Params.Workspace)
	if err != nil {
		return nil, err
	}
	if err := a.requireDAGWriteForWorkspace(ctx, workspaceName); err != nil {
		return nil, err
	}
	if err := wiki.ValidateAttachmentName(request.Params.Name); err != nil {
		return nil, &Error{
			Code:       api.ErrorCodeBadRequest,
			Message:    err.Error(),
			HTTPStatus: http.StatusBadRequest,
		}
	}
	pageID, err := scopedWikiPagePath(workspaceName, request.Params.Path)
	if err != nil {
		return nil, err
	}

	data, err := io.ReadAll(io.LimitReader(request.Body, maxWikiPageAttachmentSize+1))
	if err != nil {
		return nil, internalError(err)
	}
	if len(data) > maxWikiPageAttachmentSize {
		return nil, &Error{
			Code:       api.ErrorCodePayloadTooLarge,
			Message:    fmt.Sprintf("attachment too large (max %d bytes)", maxWikiPageAttachmentSize),
			HTTPStatus: http.StatusRequestEntityTooLarge,
		}
	}

	attachment, err := a.wikiStore.PutAttachment(ctx, pageID, request.Params.Name, bytes.NewReader(data))
	if err != nil {
		switch {
		case errors.Is(err, wiki.ErrPageNotFound):
			return nil, errWikiPageNotFound
		case errors.Is(err, wiki.ErrInvalidAttachmentName):
			return nil, &Error{
				Code:       api.ErrorCodeBadRequest,
				Message:    err.Error(),
				HTTPStatus: http.StatusBadRequest,
			}
		}
		logger.Error(ctx, "Failed to store page attachment", tag.Error(err))
		return nil, internalError(err)
	}

	a.logAudit(ctx, audit.CategoryWiki, auditActionWikiPageAttachmentUpload, map[string]any{
		"workspace": workspaceName,
		"path":      request.Params.Path,
		"name":      attachment.Name,
		"size":      attachment.Size,
	})

	return api.UploadWikiPageAttachment201JSONResponse(api.WikiPageAttachmentResponse{
		Name: attachment.Name,
		Size: attachment.Size,
	}), nil
}

// DownloadWikiPageAttachment streams a stored Wiki page attachment.
func (a *API) DownloadWikiPageAttachment(ctx context.Context, request api.DownloadWikiPageAttachmentRequestObject) (api.DownloadWikiPageAttachmentResponseObject, error) {
	if err := a.requireWikiManagement(); err != nil {
		return nil, err
	}
	a.workspaceWikiMu.RLock()
	defer a.workspaceWikiMu.RUnlock()
	pageID, err := a.wikiPointReadScopedID(ctx, request.Params.Workspace, request.Params.Path)
	if err != nil {
		return nil, err
	}
	reader, attachment, err := a.wikiStore.OpenAttachment(ctx, pageID, request.Params.Name)
	if err != nil {
		if errors.Is(err, wiki.ErrPageAttachmentNotFound) || errors.Is(err, wiki.ErrInvalidAttachmentName) {
			return nil, errWikiPageAttachmentNotFound
		}
		logger.Error(ctx, "Failed to open page attachment", tag.Error(err))
		return nil, internalError(err)
	}
	return api.DownloadWikiPageAttachment200ApplicationoctetStreamResponse{
		Body: reader,
		Headers: api.DownloadWikiPageAttachment200ResponseHeaders{
			ContentDisposition: fmt.Sprintf("attachment; filename=%q", sanitizeFilename(attachment.Name)),
		},
		ContentLength: attachment.Size,
	}, nil
}

// ListWikiPageBacklinks returns Wiki pages whose wiki links resolve to the target.
func (a *API) ListWikiPageBacklinks(ctx context.Context, request api.ListWikiPageBacklinksRequestObject) (api.ListWikiPageBacklinksResponseObject, error) {
	if err := a.requireWikiManagement(); err != nil {
		return nil, err
	}
	a.workspaceWikiMu.RLock()
	defer a.workspaceWikiMu.RUnlock()
	workspaceName, visibility, err := a.wikiPointReadScopeForParams(ctx, request.Params.Workspace)
	if err != nil {
		return nil, err
	}
	target := strings.TrimSpace(request.Params.Target)
	items := make([]api.WikiPageMetadataResponse, 0)
	if target == "" {
		return api.ListWikiPageBacklinks200JSONResponse{Items: items}, nil
	}
	// Targets without a scheme are Wiki page paths scoped to the workspace;
	// scheme-prefixed targets (dag:name) are matched verbatim.
	if !strings.Contains(target, ":") {
		if err := validateWikiPagePath(target); err != nil {
			return nil, err
		}
		target, err = scopedWikiPagePath(workspaceName, target)
		if err != nil {
			return nil, err
		}
	}

	results, err := a.wikiStore.Backlinks(ctx, target, workspaceName)
	if err != nil {
		logger.Error(ctx, "Failed to list page backlinks", tag.Error(err))
		return nil, internalError(err)
	}
	for _, m := range results {
		rawID := m.ID
		if workspaceName != "" {
			prefix := workspaceName + "/"
			if !strings.HasPrefix(m.ID, prefix) {
				continue
			}
			m.ID = strings.TrimPrefix(m.ID, prefix)
		} else if !visibility.all && !visibility.visible(m.ID) {
			continue
		}
		item := toWikiPageMetadataResponse(m)
		item.Workspace = wikiWorkspaceValue(workspaceName, rawID, visibility, false)
		items = append(items, item)
	}
	return api.ListWikiPageBacklinks200JSONResponse{Items: items}, nil
}

// SearchWikiPages searches Wiki page content.
func (a *API) SearchWikiPages(ctx context.Context, request api.SearchWikiPagesRequestObject) (api.SearchWikiPagesResponseObject, error) {
	if err := a.requireWikiManagement(); err != nil {
		return nil, err
	}
	a.workspaceWikiMu.RLock()
	defer a.workspaceWikiMu.RUnlock()

	query, err := validateSearchQuery(request.Params.Q)
	if err != nil {
		return nil, err
	}
	workspaceName, visibility, err := a.wikiReadScopeForParams(ctx, request.Params.Workspace)
	if err != nil {
		return nil, err
	}

	results, err := a.wikiStore.Search(ctx, query)
	if err != nil {
		logger.Error(ctx, "Failed to search Wiki pages", tag.Error(err))
		return nil, internalError(err)
	}

	items := make([]api.WikiPageSearchResultItem, 0, len(results))
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
		item := api.WikiPageSearchResultItem{
			Id:          r.ID,
			Title:       r.Title,
			Description: r.Description,
			Tags:        wikiPageTagsValue(r.Tags),
			Workspace:   wikiWorkspaceValue(workspaceName, rawID, visibility, false),
		}
		if r.MatchCount > 0 {
			item.MatchCount = ptrOf(r.MatchCount)
		}
		if !r.ModTime.IsZero() {
			item.ModifiedAt = ptrOf(r.ModTime)
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

	return api.SearchWikiPages200JSONResponse{
		Results: items,
	}, nil
}

// UpdateWikiPage updates Wiki page content.
func (a *API) UpdateWikiPage(ctx context.Context, request api.UpdateWikiPageRequestObject) (api.UpdateWikiPageResponseObject, error) {
	if err := a.requireWikiManagement(); err != nil {
		return nil, err
	}
	a.workspaceWikiMu.Lock()
	defer a.workspaceWikiMu.Unlock()
	if request.Body == nil {
		return nil, ErrInvalidRequestBody
	}
	workspaceName, err := wikiMutationScopeForParams(request.Params.Workspace)
	if err != nil {
		return nil, err
	}
	if err := a.requireDAGWriteForWorkspace(ctx, workspaceName); err != nil {
		return nil, err
	}
	pageID, err := a.scopedWikiPageMutationPath(ctx, workspaceName, request.Params.Path)
	if err != nil {
		return nil, err
	}
	if err := a.wikiStore.Update(ctx, pageID, request.Body.Content); err != nil {
		if errors.Is(err, wiki.ErrPageNotFound) {
			return nil, errWikiPageNotFound
		}
		logger.Error(ctx, "Failed to update page", tag.Error(err))
		return nil, internalError(err)
	}

	a.logAudit(ctx, audit.CategoryWiki, auditActionWikiPageUpdate, map[string]any{
		"doc_id":    request.Params.Path,
		"workspace": workspaceName,
	})
	a.notifyWikiMutation()

	msg := "Wiki page updated"
	return api.UpdateWikiPage200JSONResponse{Message: &msg}, nil
}

// DeleteWikiPage removes a Wiki page.
func (a *API) DeleteWikiPage(ctx context.Context, request api.DeleteWikiPageRequestObject) (api.DeleteWikiPageResponseObject, error) {
	if err := a.requireWikiManagement(); err != nil {
		return nil, err
	}
	a.workspaceWikiMu.Lock()
	defer a.workspaceWikiMu.Unlock()
	workspaceName, err := wikiMutationScopeForParams(request.Params.Workspace)
	if err != nil {
		return nil, err
	}
	if err := a.requireDAGWriteForWorkspace(ctx, workspaceName); err != nil {
		return nil, err
	}
	pageID, err := a.scopedWikiPageMutationPath(ctx, workspaceName, request.Params.Path)
	if err != nil {
		return nil, err
	}

	if err := a.wikiStore.Delete(ctx, pageID); err != nil {
		if errors.Is(err, wiki.ErrPageNotFound) {
			return nil, errWikiPageNotFound
		}
		if errors.Is(err, wiki.ErrPagePathConflict) {
			return nil, errWikiPagePathConflict
		}
		logger.Error(ctx, "Failed to delete page", tag.Error(err))
		return nil, internalError(err)
	}

	a.logAudit(ctx, audit.CategoryWiki, auditActionWikiPageDelete, map[string]any{
		"doc_id":    request.Params.Path,
		"workspace": workspaceName,
	})
	a.notifyWikiMutation()

	return api.DeleteWikiPage204Response{}, nil
}

// RenameWikiPage renames/moves a Wiki page.
func (a *API) RenameWikiPage(ctx context.Context, request api.RenameWikiPageRequestObject) (api.RenameWikiPageResponseObject, error) {
	if err := a.requireWikiManagement(); err != nil {
		return nil, err
	}
	a.workspaceWikiMu.Lock()
	defer a.workspaceWikiMu.Unlock()
	if request.Body == nil {
		return nil, ErrInvalidRequestBody
	}
	workspaceName, err := wikiMutationScopeForParams(request.Params.Workspace)
	if err != nil {
		return nil, err
	}
	if err := a.requireDAGWriteForWorkspace(ctx, workspaceName); err != nil {
		return nil, err
	}
	oldPath, err := a.scopedWikiPageMutationPath(ctx, workspaceName, request.Params.Path)
	if err != nil {
		return nil, err
	}
	newPath, err := a.scopedWikiPageMutationPath(ctx, workspaceName, request.Body.NewPath)
	if err != nil {
		return nil, err
	}

	if err := a.wikiStore.Rename(ctx, oldPath, newPath); err != nil {
		if errors.Is(err, wiki.ErrPageNotFound) {
			return nil, errWikiPageNotFound
		}
		if errors.Is(err, wiki.ErrPageAlreadyExists) {
			return nil, errWikiPageAlreadyExists
		}
		if errors.Is(err, wiki.ErrPagePathConflict) {
			return nil, errWikiPagePathConflict
		}
		logger.Error(ctx, "Failed to rename page", tag.Error(err))
		return nil, internalError(err)
	}

	a.logAudit(ctx, audit.CategoryWiki, auditActionWikiPageRename, map[string]any{
		"old_path":  request.Params.Path,
		"new_path":  request.Body.NewPath,
		"workspace": workspaceName,
	})
	a.notifyWikiMutation()

	msg := fmt.Sprintf("Wiki page renamed to %s", request.Body.NewPath)
	return api.RenameWikiPage200JSONResponse{Message: &msg}, nil
}

// DeleteWikiPageBatch deletes multiple Wiki pages or directories.
func (a *API) DeleteWikiPageBatch(ctx context.Context, request api.DeleteWikiPageBatchRequestObject) (api.DeleteWikiPageBatchResponseObject, error) {
	if err := a.requireWikiManagement(); err != nil {
		return nil, err
	}
	a.workspaceWikiMu.Lock()
	defer a.workspaceWikiMu.Unlock()
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
	workspaceName, err := wikiMutationScopeForParams(request.Params.Workspace)
	if err != nil {
		return nil, err
	}
	if err := a.requireDAGWriteForWorkspace(ctx, workspaceName); err != nil {
		return nil, err
	}
	scopedPaths := make([]string, 0, len(request.Body.Paths))
	for _, p := range request.Body.Paths {
		scoped, err := a.scopedWikiPageMutationPath(ctx, workspaceName, p)
		if err != nil {
			return nil, err
		}
		scopedPaths = append(scopedPaths, scoped)
	}

	deleted, failed, err := a.wikiStore.DeleteBatch(ctx, scopedPaths)
	if err != nil {
		logger.Error(ctx, "Failed to batch delete Wiki pages", tag.Error(err))
		return nil, internalError(err)
	}

	visibleDeleted := make([]string, 0, len(deleted))
	for _, id := range deleted {
		visibleID := visibleWikiPagePath(workspaceName, id)
		visibleDeleted = append(visibleDeleted, visibleID)
		a.logAudit(ctx, audit.CategoryWiki, auditActionWikiPageDelete, map[string]any{
			"doc_id":    visibleID,
			"workspace": workspaceName,
		})
	}

	failedItems := make([]api.WikiPageDeleteBatchFailedItem, 0, len(failed))
	for _, f := range failed {
		failedItems = append(failedItems, api.WikiPageDeleteBatchFailedItem{
			Path:  visibleWikiPagePath(workspaceName, f.ID),
			Error: f.Error,
		})
	}
	if len(visibleDeleted) > 0 {
		a.notifyWikiMutation()
	}

	msg := fmt.Sprintf("Deleted %d, failed %d", len(visibleDeleted), len(failed))
	return api.DeleteWikiPageBatch200JSONResponse{
		Deleted: visibleDeleted,
		Failed:  failedItems,
		Message: msg,
	}, nil
}

// GetWikiPageTreeData is the SSE data method for the page tree.
// Identifier format: URL query string (e.g., "page=1&perPage=200")
func (a *API) GetWikiPageTreeData(ctx context.Context, queryString string) (any, error) {
	return withDAGRunReadTimeout(ctx, dagRunReadRequestInfo{
		endpoint: "/wiki/tree",
	}, func(readCtx context.Context) (any, error) {
		a.workspaceWikiMu.RLock()
		defer a.workspaceWikiMu.RUnlock()
		if a.wikiStore == nil {
			return nil, errWikiStoreNotAvailable
		}

		params, err := url.ParseQuery(queryString)
		if err != nil {
			return nil, fmt.Errorf("invalid page tree query: %w", err)
		}

		page := parseIntParam(params.Get("page"), 1)
		perPage := max(1, min(parseIntParam(params.Get("perPage"), 200), 200))
		workspaceParam := workspaceParamFromValues(params)
		workspaceName, visibility, err := a.wikiReadScopeForParams(readCtx, workspaceParam)
		if err != nil {
			return nil, err
		}
		visiblePrefix := params.Get("prefix")
		pathPrefix, err := scopedWikiPageListPrefix(workspaceName, visiblePrefix)
		if err != nil {
			return nil, err
		}

		sortField, sortOrder := wikiSortParamsFromQuery(params)

		result, err := a.wikiStore.List(readCtx, wiki.ListPagesOptions{
			Page:             page,
			PerPage:          perPage,
			Sort:             sortField,
			Order:            sortOrder,
			PathPrefix:       pathPrefix,
			ExcludePathRoots: visibility.excludedPathRoots(),
		})
		if err != nil {
			return nil, err
		}

		tree := make([]api.WikiPageTreeNodeResponse, 0, len(result.Items))
		for _, node := range result.Items {
			restoreWikiTreePrefix(node, visiblePrefix)
			tree = append(tree, toWikiPageTreeResponseWithWorkspace(node, workspaceName, visibility))
		}

		return api.ListWikiPages200JSONResponse{
			Tree:       &tree,
			Pagination: toPagination(*result),
		}, nil
	})
}

// GetWikiPageContentData is the SSE data method for page content.
func (a *API) GetWikiPageContentData(ctx context.Context, pageID string) (any, error) {
	return withDAGRunReadTimeout(ctx, dagRunReadRequestInfo{
		endpoint: "/wiki/{pageID}",
	}, func(readCtx context.Context) (any, error) {
		a.workspaceWikiMu.RLock()
		defer a.workspaceWikiMu.RUnlock()
		if a.wikiStore == nil {
			return nil, errWikiStoreNotAvailable
		}
		path, queryString, hasQuery := strings.Cut(pageID, "?")
		var (
			workspaceName string
			visibility    wikiWorkspaceVisibility
			err           error
			params        url.Values
		)
		if hasQuery {
			params, err = url.ParseQuery(queryString)
			if err != nil {
				return nil, err
			}
			workspaceParam := workspaceParamFromValues(params)
			workspaceName, visibility, err = a.wikiPointReadScopeForParams(readCtx, workspaceParam)
			if err != nil {
				return nil, err
			}
		} else {
			workspaceName, visibility, err = a.wikiPointReadScopeForParams(readCtx, nil)
			if err != nil {
				return nil, err
			}
		}
		scopedID, err := scopedWikiPagePath(workspaceName, path)
		if err != nil {
			return nil, err
		}
		page, err := a.wikiStore.Get(readCtx, scopedID)
		if err != nil {
			if errors.Is(err, wiki.ErrPageNotFound) {
				return nil, errWikiPageNotFound
			}
			return nil, internalError(err)
		}
		if workspaceName == "" && !visibility.all {
			if !visibility.visible(page.ID) {
				return nil, errWikiPageNotFound
			}
		}
		rawID := page.ID
		page.ID = visibleWikiPagePath(workspaceName, page.ID)
		resp := toWikiPageResponse(page)
		resp.Workspace = wikiWorkspaceValue(workspaceName, rawID, visibility, false)
		return resp, nil
	})
}

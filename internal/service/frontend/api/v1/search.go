// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/dagstore"
	"github.com/dagucloud/dagu/v2/internal/pagination"
	"github.com/dagucloud/dagu/v2/internal/wiki"
)

const (
	searchDefaultLimit        = 20
	searchDefaultMatchLimit   = 5
	searchMaxLimit            = 50
	searchPreviewMatchesLimit = 1
)

func validateSearchQuery(query string) (string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", &Error{
			Code:       api.ErrorCodeBadRequest,
			Message:    "query parameter 'q' is required",
			HTTPStatus: http.StatusBadRequest,
		}
	}
	return query, nil
}

func normalizeSearchLimit(limit int, defaultValue int) int {
	if limit <= 0 {
		limit = defaultValue
	}
	if limit > searchMaxLimit {
		limit = searchMaxLimit
	}
	return limit
}

func invalidSearchCursorError() error {
	return &Error{
		Code:       api.ErrorCodeBadRequest,
		Message:    "invalid search cursor",
		HTTPStatus: http.StatusBadRequest,
	}
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return ptrOf(value)
}

func scopedDAGSearchLabels(labelsParam *string) []string {
	return parseCommaSeparatedLabels(labelsParam)
}

func toSearchMatchItems(matches []*dagstore.Match) []api.SearchMatchItem {
	items := make([]api.SearchMatchItem, 0, len(matches))
	for _, match := range matches {
		items = append(items, api.SearchMatchItem{
			Line:       match.Line,
			LineNumber: match.LineNumber,
			StartLine:  match.StartLine,
		})
	}
	return items
}

func mapCursorItems[TIn any, TOut any](result *pagination.CursorResult[TIn], mapItem func(TIn) TOut) ([]TOut, bool, *string) {
	items := make([]TOut, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, mapItem(item))
	}
	return items, result.HasMore, optionalString(result.NextCursor)
}

func toDAGSearchPageItem(item dagstore.SearchDAGResult) api.DAGSearchPageItem {
	name := item.Name
	if name == "" {
		// File-backed DAG search uses the DAG file name as its display label.
		name = item.FileName
	}
	return api.DAGSearchPageItem{
		FileName:          item.FileName,
		Name:              name,
		Workspace:         optionalString(item.Workspace),
		HasMoreMatches:    item.HasMoreMatches,
		NextMatchesCursor: optionalString(item.NextMatchesCursor),
		Matches:           toSearchMatchItems(item.Matches),
	}
}

func toDAGSearchFeedResponse(result *pagination.CursorResult[dagstore.SearchDAGResult]) api.DAGSearchFeedResponse {
	items, hasMore, nextCursor := mapCursorItems(result, toDAGSearchPageItem)
	return api.DAGSearchFeedResponse{
		Results:    items,
		HasMore:    hasMore,
		NextCursor: nextCursor,
	}
}

func toWikiPageSearchPageItem(
	item wiki.PageSearchResult,
	workspaceName string,
	visibility wikiWorkspaceVisibility,
) api.WikiPageSearchPageItem {
	result := api.WikiPageSearchPageItem{
		Id:                item.ID,
		Title:             item.Title,
		Description:       item.Description,
		Tags:              wikiPageTagsValue(item.Tags),
		Workspace:         wikiWorkspaceValue(workspaceName, item.ID, visibility, false),
		HasMoreMatches:    item.HasMoreMatches,
		NextMatchesCursor: optionalString(item.NextMatchesCursor),
		Matches:           toSearchMatchItems(item.Matches),
	}
	if !item.ModTime.IsZero() {
		result.ModifiedAt = ptrOf(item.ModTime)
	}
	return result
}

func toWikiPageSearchFeedResponse(
	result *pagination.CursorResult[wiki.PageSearchResult],
	workspaceName string,
	visibility wikiWorkspaceVisibility,
) api.WikiPageSearchFeedResponse {
	items, hasMore, nextCursor := mapCursorItems(result, func(item wiki.PageSearchResult) api.WikiPageSearchPageItem {
		return toWikiPageSearchPageItem(item, workspaceName, visibility)
	})
	return api.WikiPageSearchFeedResponse{
		Results:    items,
		HasMore:    hasMore,
		NextCursor: nextCursor,
	}
}

func toSearchMatchesResponse(result *pagination.CursorResult[*dagstore.Match]) api.SearchMatchesResponse {
	return api.SearchMatchesResponse{
		Matches:    toSearchMatchItems(result.Items),
		HasMore:    result.HasMore,
		NextCursor: optionalString(result.NextCursor),
	}
}

// SearchDAGFeed returns cursor-based DAG search results for the global search page.
func (a *API) SearchDAGFeed(ctx context.Context, request api.SearchDAGFeedRequestObject) (api.SearchDAGFeedResponseObject, error) {
	query, err := validateSearchQuery(request.Params.Q)
	if err != nil {
		return nil, err
	}
	labels := scopedDAGSearchLabels(request.Params.Labels)
	workspaceFilter, err := a.workspaceFilterForParams(ctx, request.Params.Workspace)
	if err != nil {
		return nil, err
	}

	result, errs, err := a.dagStore.SearchCursor(ctx, dagstore.SearchDAGsOptions{
		Cursor:          valueOf(request.Params.Cursor),
		Limit:           normalizeSearchLimit(valueOf(request.Params.Limit), searchDefaultLimit),
		Query:           query,
		MatchLimit:      searchPreviewMatchesLimit,
		Labels:          labels,
		WorkspaceFilter: workspaceFilter,
	})
	if err != nil {
		if errors.Is(err, pagination.ErrInvalidCursor) {
			return nil, invalidSearchCursorError()
		}
		logger.Error(ctx, "Failed to search DAGs", tag.Error(err))
		return nil, internalError(err)
	}
	for _, searchErr := range errs {
		logger.Warn(ctx, "Skipped DAG while searching", tag.Reason(searchErr))
	}

	return api.SearchDAGFeed200JSONResponse(toDAGSearchFeedResponse(result)), nil
}

// SearchWikiPageFeed returns cursor-based Wiki page search results for the global search page.
func (a *API) SearchWikiPageFeed(ctx context.Context, request api.SearchWikiPageFeedRequestObject) (api.SearchWikiPageFeedResponseObject, error) {
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
	filterPrefix := string(valueOf(request.Params.Prefix))
	if filterPrefix != "" {
		if err := validateWikiPagePath(filterPrefix); err != nil {
			return nil, err
		}
	}

	result, err := a.wikiStore.SearchCursor(ctx, wiki.SearchPagesOptions{
		Cursor:           valueOf(request.Params.Cursor),
		Limit:            normalizeSearchLimit(valueOf(request.Params.Limit), searchDefaultLimit),
		Query:            query,
		MatchLimit:       searchPreviewMatchesLimit,
		PathPrefix:       workspaceName,
		FilterPrefix:     filterPrefix,
		Tags:             valueOf(request.Params.Tags),
		ExcludePathRoots: visibility.excludedPathRoots(),
	})
	if err != nil {
		if errors.Is(err, pagination.ErrInvalidCursor) {
			return nil, invalidSearchCursorError()
		}
		logger.Error(ctx, "Failed to search Wiki pages", tag.Error(err))
		return nil, internalError(err)
	}
	if workspaceName == "" && !visibility.all {
		items := result.Items[:0]
		for _, item := range result.Items {
			if visibility.visible(item.ID) {
				items = append(items, item)
			}
		}
		result.Items = items
	}

	return api.SearchWikiPageFeed200JSONResponse(toWikiPageSearchFeedResponse(result, workspaceName, visibility)), nil
}

// SearchDagMatches returns cursor-based snippets for one DAG result.
func (a *API) SearchDagMatches(ctx context.Context, request api.SearchDagMatchesRequestObject) (api.SearchDagMatchesResponseObject, error) {
	query, err := validateSearchQuery(request.Params.Q)
	if err != nil {
		return nil, err
	}
	labels := scopedDAGSearchLabels(request.Params.Labels)
	workspaceFilter, err := a.workspaceFilterForParams(ctx, request.Params.Workspace)
	if err != nil {
		return nil, err
	}

	result, err := a.dagStore.SearchMatches(ctx, request.FileName, dagstore.SearchDAGMatchesOptions{
		Cursor:          valueOf(request.Params.Cursor),
		Limit:           normalizeSearchLimit(valueOf(request.Params.Limit), searchDefaultMatchLimit),
		Query:           query,
		Labels:          labels,
		WorkspaceFilter: workspaceFilter,
	})
	if err != nil {
		switch {
		case errors.Is(err, dagstore.ErrDAGNotFound):
			return nil, &Error{
				Code:       api.ErrorCodeNotFound,
				Message:    "DAG not found",
				HTTPStatus: http.StatusNotFound,
			}
		case errors.Is(err, pagination.ErrInvalidCursor):
			return nil, invalidSearchCursorError()
		default:
			logger.Error(ctx, "Failed to search DAG matches", tag.Name(request.FileName), tag.Error(err))
			return nil, internalError(err)
		}
	}

	return api.SearchDagMatches200JSONResponse(toSearchMatchesResponse(result)), nil
}

// SearchWikiPageMatches returns cursor-based snippets for one Wiki page result.
func (a *API) SearchWikiPageMatches(ctx context.Context, request api.SearchWikiPageMatchesRequestObject) (api.SearchWikiPageMatchesResponseObject, error) {
	if err := a.requireWikiManagement(); err != nil {
		return nil, err
	}
	a.workspaceWikiMu.RLock()
	defer a.workspaceWikiMu.RUnlock()
	if err := validateWikiPagePath(request.Params.Path); err != nil {
		return nil, err
	}
	workspaceName, visibility, err := a.wikiPointReadScopeForParams(ctx, request.Params.Workspace)
	if err != nil {
		return nil, err
	}
	if workspaceName == "" && !visibility.all {
		if !visibility.visible(request.Params.Path) {
			return nil, errWikiPageNotFound
		}
	}

	query, err := validateSearchQuery(request.Params.Q)
	if err != nil {
		return nil, err
	}

	cursor := valueOf(request.Params.Cursor)
	matchOpts := wiki.SearchPageMatchesOptions{
		Cursor:     cursor,
		Limit:      normalizeSearchLimit(valueOf(request.Params.Limit), searchDefaultMatchLimit),
		Query:      query,
		PathPrefix: workspaceName,
	}
	result, err := a.wikiStore.SearchMatches(ctx, request.Params.Path, matchOpts)
	if err != nil && errors.Is(err, pagination.ErrInvalidCursor) && workspaceName != "" && cursor != "" {
		// Aggregate-search cursors encode an empty path prefix. Replaying the
		// workspace-qualified ID preserves the authorized Wiki page scope.
		aggregatePath, scopeErr := scopedWikiPagePath(workspaceName, request.Params.Path)
		if scopeErr == nil {
			aggregateOpts := matchOpts
			aggregateOpts.PathPrefix = ""
			if aggregateResult, aggregateErr := a.wikiStore.SearchMatches(ctx, aggregatePath, aggregateOpts); aggregateErr == nil {
				return api.SearchWikiPageMatches200JSONResponse(toSearchMatchesResponse(aggregateResult)), nil
			}
		}
	}
	if err != nil {
		switch {
		case errors.Is(err, wiki.ErrPageNotFound):
			return nil, errWikiPageNotFound
		case errors.Is(err, pagination.ErrInvalidCursor):
			return nil, invalidSearchCursorError()
		default:
			logger.Error(ctx, "Failed to search page matches", tag.Name(request.Params.Path), tag.Error(err))
			return nil, internalError(err)
		}
	}

	return api.SearchWikiPageMatches200JSONResponse(toSearchMatchesResponse(result)), nil
}

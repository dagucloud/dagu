// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	api "github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core/exec"
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

func toSearchMatchItems(matches []*exec.Match) []api.SearchMatchItem {
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

func mapCursorItems[TIn any, TOut any](result *exec.CursorResult[TIn], mapItem func(TIn) TOut) ([]TOut, bool, *string) {
	items := make([]TOut, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, mapItem(item))
	}
	return items, result.HasMore, optionalString(result.NextCursor)
}

func toDAGSearchPageItem(item exec.SearchDAGResult) api.DAGSearchPageItem {
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

func toDAGSearchFeedResponse(result *exec.CursorResult[exec.SearchDAGResult]) api.DAGSearchFeedResponse {
	items, hasMore, nextCursor := mapCursorItems(result, toDAGSearchPageItem)
	return api.DAGSearchFeedResponse{
		Results:    items,
		HasMore:    hasMore,
		NextCursor: nextCursor,
	}
}

func toSearchMatchesResponse(result *exec.CursorResult[*exec.Match]) api.SearchMatchesResponse {
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

	result, errs, err := a.dagStore.SearchCursor(ctx, exec.SearchDAGsOptions{
		Cursor:          valueOf(request.Params.Cursor),
		Limit:           normalizeSearchLimit(valueOf(request.Params.Limit), searchDefaultLimit),
		Query:           query,
		MatchLimit:      searchPreviewMatchesLimit,
		Labels:          labels,
		WorkspaceFilter: workspaceFilter,
	})
	if err != nil {
		if errors.Is(err, exec.ErrInvalidCursor) {
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

	result, err := a.dagStore.SearchMatches(ctx, request.FileName, exec.SearchDAGMatchesOptions{
		Cursor:          valueOf(request.Params.Cursor),
		Limit:           normalizeSearchLimit(valueOf(request.Params.Limit), searchDefaultMatchLimit),
		Query:           query,
		Labels:          labels,
		WorkspaceFilter: workspaceFilter,
	})
	if err != nil {
		switch {
		case errors.Is(err, exec.ErrDAGNotFound):
			return nil, &Error{
				Code:       api.ErrorCodeNotFound,
				Message:    "DAG not found",
				HTTPStatus: http.StatusNotFound,
			}
		case errors.Is(err, exec.ErrInvalidCursor):
			return nil, invalidSearchCursorError()
		default:
			logger.Error(ctx, "Failed to search DAG matches", tag.Name(request.FileName), tag.Error(err))
			return nil, internalError(err)
		}
	}

	return api.SearchDagMatches200JSONResponse(toSearchMatchesResponse(result)), nil
}

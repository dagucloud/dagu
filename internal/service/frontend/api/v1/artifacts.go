// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

// ListArtifacts implements api.StrictServerInterface.
func (a *API) ListArtifacts(
	ctx context.Context, request api.ListArtifactsRequestObject,
) (api.ListArtifactsResponseObject, error) {
	workspaceFilter, err := a.workspaceFilterForParams(ctx, request.Params.Workspace)
	if err != nil {
		return nil, err
	}

	query := persis.ArtifactQuery{WorkspaceFilter: workspaceFilter}
	if request.Params.Name != nil {
		query.Name = *request.Params.Name
	}
	if request.Params.FileName != nil {
		query.FileName = *request.Params.FileName
	}
	if request.Params.FromDate != nil {
		query.From = persis.NewUTC(time.Unix(*request.Params.FromDate, 0))
	}
	if request.Params.ToDate != nil {
		query.To = persis.NewUTC(time.Unix(*request.Params.ToDate, 0))
	}
	if request.Params.Limit != nil {
		query.Limit = *request.Params.Limit
	}
	if request.Params.Cursor != nil {
		query.Cursor = *request.Params.Cursor
	}

	page, err := a.artifactRepository.List(ctx, query)
	if err != nil {
		if errors.Is(err, persis.ErrInvalidArtifactCursor) ||
			errors.Is(err, persis.ErrInvalidArtifactFileName) {
			return nil, &Error{
				HTTPStatus: http.StatusBadRequest,
				Code:       api.ErrorCodeBadRequest,
				Message:    err.Error(),
			}
		}
		return nil, fmt.Errorf("error listing artifacts: %w", err)
	}

	items := make([]api.ArtifactListItem, 0, len(page.Items))
	for _, run := range page.Items {
		files := make([]api.ArtifactListFile, 0, len(run.Files))
		for _, f := range run.Files {
			files = append(files, api.ArtifactListFile{Path: f.Path, Size: f.Size})
		}
		entry := api.ArtifactListItem{
			Name:           run.Name,
			DagRunId:       run.DAGRunID,
			CreatedAt:      stringutil.FormatTime(run.CreatedAt),
			Files:          files,
			FilesTruncated: run.FilesTruncated,
		}
		if !run.StartedAt.IsZero() {
			entry.StartedAt = ptrOf(stringutil.FormatTime(run.StartedAt))
		}
		items = append(items, entry)
	}

	response := api.ArtifactListResponse{Items: items}
	if page.NextCursor != "" {
		response.NextCursor = ptrOf(page.NextCursor)
	}
	return api.ListArtifacts200JSONResponse(response), nil
}

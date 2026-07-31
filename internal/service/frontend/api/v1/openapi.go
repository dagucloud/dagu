// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagucloud/dagu/v2/api/v1"
)

func (a *API) GetOpenapiJson(ctx context.Context, _ api.GetOpenapiJsonRequestObject) (api.GetOpenapiJsonResponseObject, error) {
	swagger, err := a.loadOpenAPISpec(ctx)
	if err != nil {
		return nil, err
	}

	doc, err := toOpenAPIResponse(swagger)
	if err != nil {
		return nil, err
	}

	return api.GetOpenapiJson200JSONResponse(doc), nil
}

func toOpenAPIResponse(v any) (map[string]any, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal openapi document: %w", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("failed to unmarshal openapi document: %w", err)
	}
	return doc, nil
}

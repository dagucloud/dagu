// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"

	"github.com/dagucloud/dagu/internal/cmn/datapath"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
)

// resolveJSONPath extracts a value from JSON data using a jq-style path.
func resolveJSONPath(ctx context.Context, varName, jsonStr, path string) (string, bool) {
	raw, ok := parseJSONValue(ctx, varName, jsonStr)
	if !ok {
		return "", false
	}
	value, ok := datapath.Select(ctx, varName, raw, path)
	if !ok {
		return "", false
	}
	return stringifyResolvedValue(value), true
}

func parseJSONValue(ctx context.Context, varName, jsonStr string) (any, bool) {
	var raw any
	if err := json.Unmarshal([]byte(jsonStr), &raw); err != nil {
		logger.Warn(ctx, "Failed to parse JSON",
			slog.String("var", varName),
			tag.Error(err))
		return nil, false
	}
	return raw, true
}

func stringifyResolvedValue(value any) string {
	if value == nil {
		return fmt.Sprintf("%v", value)
	}
	switch value.(type) {
	case map[string]any, []any:
		if data, err := json.Marshal(value); err == nil {
			return string(data)
		}
	}
	rv := reflect.ValueOf(value)
	//nolint:exhaustive // Only collection kinds need JSON stringification; primitives fall through to fmt.
	switch rv.Kind() {
	case reflect.Map, reflect.Slice, reflect.Array:
		if data, err := json.Marshal(value); err == nil {
			return string(data)
		}
	}
	return fmt.Sprintf("%v", value)
}

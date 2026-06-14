// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"

	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/itchyny/gojq"
)

// resolveJSONPath extracts a value from JSON data using a jq-style path.
func resolveJSONPath(ctx context.Context, varName, jsonStr, path string) (string, bool) {
	raw, ok := parseJSONValue(ctx, varName, jsonStr)
	if !ok {
		return "", false
	}
	value, ok := ResolveDataPath(ctx, varName, raw, path)
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

// ResolveDataPath extracts a value from structured data using a jq-style path.
func ResolveDataPath(ctx context.Context, varName string, raw any, path string) (any, bool) {
	query, err := gojq.Parse(path)
	if err != nil {
		logger.Warn(ctx, "Failed to parse path in data",
			tag.Path(path),
			slog.String("var", varName),
			tag.Error(err))
		return nil, false
	}

	iter := query.Run(raw)
	v, ok := iter.Next()
	if !ok {
		return nil, false
	}

	if evalErr, ok := v.(error); ok {
		logger.Warn(ctx, "Failed to evaluate path in data",
			tag.Path(path),
			slog.String("var", varName),
			tag.Error(evalErr))
		return nil, false
	}

	return v, true
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

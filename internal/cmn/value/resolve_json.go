// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/datapath"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
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
	decoder := json.NewDecoder(strings.NewReader(jsonStr))
	decoder.UseNumber()
	err := decoder.Decode(&raw)
	if err == nil && decoder.Decode(new(any)) != io.EOF {
		err = fmt.Errorf("expected one JSON value")
	}
	if err != nil {
		logger.Warn(ctx, "Failed to parse JSON",
			slog.String("var", varName),
			tag.Error(err))
		return nil, false
	}
	return NormalizeJSONNumbers(raw), true
}

// maxExactInt is the largest magnitude a float64 represents without rounding.
const maxExactInt = 1 << 53

// NormalizeJSONNumbers converts values decoded as json.Number to float64,
// keeping the literal only for integers a float64 cannot hold exactly. Maps and
// slices are normalized in place.
func NormalizeJSONNumbers(v any) any {
	switch v := v.(type) {
	case json.Number:
		if strings.ContainsAny(v.String(), ".eE") {
			if f, err := v.Float64(); err == nil {
				return f
			}
			return v
		}
		if i, err := v.Int64(); err == nil && i <= maxExactInt && i >= -maxExactInt {
			return float64(i)
		}
		return v
	case map[string]any:
		for key, val := range v {
			v[key] = NormalizeJSONNumbers(val)
		}
		return v
	case []any:
		for i, val := range v {
			v[i] = NormalizeJSONNumbers(val)
		}
		return v
	default:
		return v
	}
}

// marshalUnescaped serializes a value as JSON, leaving <, > and & as the
// characters the value holds rather than as escape sequences.
func marshalUnescaped(value any) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func stringifyResolvedValue(value any) string {
	if value == nil {
		return fmt.Sprintf("%v", value)
	}
	switch value.(type) {
	case map[string]any, []any:
		if data, err := marshalUnescaped(value); err == nil {
			return string(data)
		}
	}
	rv := reflect.ValueOf(value)
	//nolint:exhaustive // Only collection kinds need JSON stringification; primitives fall through to fmt.
	switch rv.Kind() {
	case reflect.Map, reflect.Slice, reflect.Array:
		if data, err := marshalUnescaped(value); err == nil {
			return string(data)
		}
	}
	return fmt.Sprintf("%v", value)
}

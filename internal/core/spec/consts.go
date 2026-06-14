// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strconv"

	"github.com/dagucloud/dagu/internal/cmn/eval"
	"github.com/dagucloud/dagu/internal/core"
)

var constNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

func buildConsts(_ BuildContext, d *dag) (map[string]any, error) {
	if d.Consts == nil {
		return nil, nil
	}
	items, ok := d.Consts.([]any)
	if !ok {
		return nil, core.NewValidationError("consts", d.Consts, fmt.Errorf("consts must use list form"))
	}

	resolved := make(map[string]any, len(items))
	for idx, item := range items {
		key, value, err := constEntry(idx, item)
		if err != nil {
			return nil, err
		}
		if _, exists := resolved[key]; exists {
			return nil, core.NewValidationError("consts."+key, key, fmt.Errorf("consts key %q is defined more than once", key))
		}
		resolvedValue, err := resolveConstValue(key, value, resolved)
		if err != nil {
			return nil, core.NewValidationError("consts."+key, value, err)
		}
		resolved[key] = resolvedValue
	}
	return resolved, nil
}

func constEntry(idx int, item any) (string, any, error) {
	entry, ok := item.(map[string]any)
	if !ok {
		return "", nil, core.NewValidationError(
			fmt.Sprintf("consts[%d]", idx),
			item,
			fmt.Errorf("consts entries must be single-entry mappings"),
		)
	}
	if len(entry) != 1 {
		return "", nil, core.NewValidationError(
			fmt.Sprintf("consts[%d]", idx),
			item,
			fmt.Errorf("consts entries must contain exactly one key"),
		)
	}
	for key, value := range entry {
		if constNamePattern.MatchString(key) {
			return key, value, nil
		}
		return "", nil, core.NewValidationError(
			fmt.Sprintf("consts[%d]", idx),
			key,
			fmt.Errorf("consts key %q is invalid", key),
		)
	}
	return "", nil, core.NewValidationError(fmt.Sprintf("consts[%d]", idx), item, fmt.Errorf("consts entries must contain exactly one key"))
}

func resolveConstValue(key string, value any, consts map[string]any) (any, error) {
	switch v := value.(type) {
	case string:
		resolved, err := eval.ParseTemplate(v).Resolve(eval.Scope{Consts: eval.Values(consts)})
		if err != nil {
			return nil, fmt.Errorf("failed to resolve const %q: %w", key, err)
		}
		return resolved, nil
	case bool:
		return v, nil
	case json.Number:
		f, err := strconv.ParseFloat(v.String(), 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, fmt.Errorf("const %q must be finite", key)
		}
		return v, nil
	}

	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return value, nil
	case reflect.Float32, reflect.Float64:
		f := rv.Convert(reflect.TypeFor[float64]()).Float()
		if !math.IsNaN(f) && !math.IsInf(f, 0) {
			return value, nil
		}
	}
	return nil, fmt.Errorf("const %q must be a literal string, number, or boolean", key)
}

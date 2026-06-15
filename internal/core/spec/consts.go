// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"reflect"
	"regexp"
	"strconv"

	cmnvalue "github.com/dagucloud/dagu/internal/cmn/value"
	"github.com/dagucloud/dagu/internal/core"
)

var constNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

func buildConsts(ctx BuildContext, d *dag) (map[string]any, error) {
	inherited := inheritedConsts(ctx)
	if d.Consts == nil {
		return inherited, nil
	}
	items, ok := d.Consts.([]any)
	if !ok {
		return nil, core.NewValidationError("consts", d.Consts, fmt.Errorf("consts must use list form"))
	}

	resolved := inherited
	if resolved == nil {
		resolved = make(map[string]any, len(items))
	}
	seen := make(map[string]struct{}, len(items))
	for idx, item := range items {
		key, value, err := constEntry(idx, item)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[key]; exists {
			return nil, core.NewValidationError("consts."+key, key, fmt.Errorf("consts key %q is defined more than once", key))
		}
		seen[key] = struct{}{}
		resolvedValue, err := resolveConstValue(key, value, resolved)
		if err != nil {
			return nil, core.NewValidationError("consts."+key, value, err)
		}
		resolved[key] = resolvedValue
	}
	if ctx.envScope != nil {
		ctx.envScope.consts = resolved
	}
	return resolved, nil
}

func inheritedConsts(ctx BuildContext) map[string]any {
	if ctx.envScope != nil && len(ctx.envScope.consts) > 0 {
		return maps.Clone(ctx.envScope.consts)
	}
	if ctx.baseDAG != nil && len(ctx.baseDAG.Consts) > 0 {
		return maps.Clone(ctx.baseDAG.Consts)
	}
	return nil
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
	if value == nil {
		return nil, fmt.Errorf("const %q must be a literal string, number, or boolean", key)
	}

	switch v := value.(type) {
	case string:
		resolver := cmnvalue.NewResolver(
			cmnvalue.StaticScope{Consts: cmnvalue.Values(consts)},
			cmnvalue.RuntimeScope{Consts: cmnvalue.Values(consts)},
		)
		resolved, err := resolver.String(context.Background(), v, cmnvalue.ConstLoadField("consts."+key))
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
	//nolint:exhaustive // Consts only accept numeric reflect kinds beyond the concrete cases above.
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

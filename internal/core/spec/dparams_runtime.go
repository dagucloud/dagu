// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/dagucloud/dagu/internal/cmn/eval"
	"github.com/dagucloud/dagu/internal/core"
)

const maxInt64AsUint = ^uint64(0) >> 1
const maxIntValue = int(^uint(0) >> 1)
const maxSafeFloat64Integer = 1 << 53

// ResolveRuntimeParamsOptions controls how a DAG is reloaded for runtime param validation.
type ResolveRuntimeParamsOptions struct {
	BaseConfig string
}

// ResolveRuntimeParams reloads a DAG from its source with runtime params applied.
// It is intended for entry points that need the same coercion and validation path
// as execution without duplicating loader setup.
func ResolveRuntimeParams(ctx context.Context, dag *core.DAG, params any, opts ResolveRuntimeParamsOptions) (*core.DAG, error) {
	if dag == nil {
		return nil, nil
	}

	loadOpts, err := runtimeParamLoadOptions(dag, params, opts)
	if err != nil {
		return nil, err
	}

	switch {
	case dag.Location != "":
		return Load(ctx, dag.Location, loadOpts...)
	case len(dag.YamlData) > 0:
		return LoadYAML(ctx, dag.YamlData, loadOpts...)
	default:
		return nil, fmt.Errorf("DAG source is required to resolve runtime params")
	}
}

func runtimeParamLoadOptions(dag *core.DAG, params any, opts ResolveRuntimeParamsOptions) ([]LoadOption, error) {
	loadOpts := make([]LoadOption, 0, 3)

	switch value := params.(type) {
	case nil:
		loadOpts = append(loadOpts, WithParams(""))
	case string:
		loadOpts = append(loadOpts, WithParams(value))
	case []string:
		loadOpts = append(loadOpts, WithParams(QuoteRuntimeParams(value, dag.ParamDefs)))
	default:
		return nil, fmt.Errorf("invalid runtime params type %T", params)
	}

	if dag.Name != "" {
		loadOpts = append(loadOpts, WithName(dag.Name))
	}
	if opts.BaseConfig != "" {
		loadOpts = append(loadOpts, WithBaseConfig(opts.BaseConfig))
	}
	if len(dag.BaseConfigData) > 0 {
		loadOpts = append(loadOpts, WithBaseConfigContent(dag.BaseConfigData))
	}

	return loadOpts, nil
}

func resolveLegacyRuntimePairs(entries []dagParamEntry, rawParams string, paramsList []string) ([]paramPair, error) {
	finalPairs := runtimePairsFromEntries(entries)
	declaredNames := declaredRuntimeParamNames(entries)

	if rawParams != "" {
		overridePairs, err := parseRuntimeLegacyOverrideInput(rawParams)
		if err != nil {
			return nil, core.NewValidationError("params", rawParams, fmt.Errorf("%w: %s", ErrInvalidParamValue, err))
		}
		overridePairs, internalPairs := splitInternalRuntimeOverridePairs(overridePairs, declaredNames)
		if err := overrideParams(&finalPairs, overridePairs); err != nil {
			return nil, err
		}
		finalPairs = appendInternalRuntimePairs(finalPairs, internalPairs)
	}

	if len(paramsList) > 0 {
		overridePairs, err := parseRuntimeLegacyOverrideInput(paramsList)
		if err != nil {
			return nil, core.NewValidationError("params", paramsList, fmt.Errorf("%w: %s", ErrInvalidParamValue, err))
		}
		overridePairs, internalPairs := splitInternalRuntimeOverridePairs(overridePairs, declaredNames)
		if err := overrideParams(&finalPairs, overridePairs); err != nil {
			return nil, err
		}
		finalPairs = appendInternalRuntimePairs(finalPairs, internalPairs)
	}

	return finalPairs, nil
}

func parseRuntimeLegacyOverrideInput(value any) ([]paramPair, error) {
	var (
		pairs []paramPair
		envs  []string
	)
	if err := parseParams(value, &pairs, &envs); err != nil {
		return nil, err
	}
	return pairs, nil
}

func resolveLegacyEntries(ctx BuildContext, plan *dagParamPlan, rawParams string, paramsList []string, metadataMode bool) ([]dagParamEntry, error) {
	overridePairs, err := parseOverridePairs(rawParams, paramsList)
	if err != nil {
		return nil, err
	}
	overridePairs, internalPairs := splitInternalRuntimeOverridePairs(overridePairs, declaredRuntimeParamNamesForPlan(plan))

	entries, overridden, err := applyOverridePairsTracked(plan.entries, overridePairs)
	if err != nil {
		return nil, err
	}

	scope := buildParamEvalScope(ctx)
	for i := range entries {
		if i < len(plan.entries) {
			if err := resolveLegacyEntry(ctx, &entries[i], plan.entries[i], overridden[i], &scope, i); err != nil {
				return nil, err
			}
			continue
		}
		addEntryToParamScope(&scope, entries[i], i)
	}

	if plan.schema == nil {
		return appendInternalRuntimeEntries(entries, internalPairs), nil
	}

	entries, err = validateSchemaBackedEntries(entries, plan.schema, plan.schemaProperties, plan.schemaOrder, metadataMode, false)
	if err != nil {
		return nil, err
	}

	return appendInternalRuntimeEntries(entries, internalPairs), nil
}

func parseOverridePairs(rawParams string, paramsList []string) ([]paramPair, error) {
	noEvalCtx := BuildContext{opts: BuildOpts{Flags: BuildFlagNoEval}}
	var pairs []paramPair
	if rawParams != "" {
		parsed, err := parseParamValue(noEvalCtx, rawParams)
		if err != nil {
			return nil, core.NewValidationError("params", rawParams, fmt.Errorf("%w: %s", ErrInvalidParamValue, err))
		}
		pairs = append(pairs, parsed...)
	}
	if len(paramsList) > 0 {
		parsed, err := parseParamValue(noEvalCtx, paramsList)
		if err != nil {
			return nil, core.NewValidationError("params", paramsList, fmt.Errorf("%w: %s", ErrInvalidParamValue, err))
		}
		pairs = append(pairs, parsed...)
	}
	return pairs, nil
}

func applyOverridePairsTracked(entries []dagParamEntry, override []paramPair) ([]dagParamEntry, []bool, error) {
	if err := rejectUnknownNamedParamsForEntries(entries, override); err != nil {
		return nil, nil, err
	}

	result := cloneParamEntries(entries)
	overridden := make([]bool, len(result))
	positionalIndex := 0

	for _, pair := range override {
		if pair.Name == "" {
			if len(entries) == 0 {
				result = append(result, dagParamEntry{Value: pair.Value, HasValue: true})
				overridden = append(overridden, true)
				continue
			}
			if positionalIndex >= len(entries) {
				return nil, nil, fmt.Errorf("too many positional params: expected at most %d, got %d", len(entries), positionalIndex+1)
			}
			result[positionalIndex].Value = pair.Value
			result[positionalIndex].HasValue = true
			overridden[positionalIndex] = true
			positionalIndex++
			continue
		}

		found := false
		for i := range result {
			if result[i].Name != pair.Name {
				continue
			}
			result[i].Value = pair.Value
			result[i].HasValue = true
			overridden[i] = true
			found = true
			break
		}
		if !found {
			result = append(result, dagParamEntry{Name: pair.Name, Value: pair.Value, HasValue: true})
			overridden = append(overridden, true)
		}
	}

	return result, overridden, nil
}

// rejectUnknownNamedParamsForEntries checks that all named overrides match a
// declared entry name. Only enforced when at least one entry has a non-empty,
// non-numeric Name (the DAG declares named params).
func rejectUnknownNamedParamsForEntries(entries []dagParamEntry, overrides []paramPair) error {
	declaredNames := make(map[string]struct{})
	for _, e := range entries {
		if e.Name != "" && !isPositionalName(e.Name) {
			declaredNames[e.Name] = struct{}{}
		}
	}
	if len(declaredNames) == 0 {
		return nil
	}

	var unknown []string
	for _, p := range overrides {
		if p.Name == "" || isPositionalName(p.Name) {
			continue
		}
		if _, ok := declaredNames[p.Name]; !ok {
			unknown = append(unknown, p.Name)
		}
	}
	if len(unknown) == 0 {
		return nil
	}

	accepted := make([]string, 0, len(declaredNames))
	for name := range declaredNames {
		accepted = append(accepted, name)
	}
	sort.Strings(accepted)

	return fmt.Errorf(
		"unknown parameter(s): %s; accepted parameters are: %s",
		quotedNames(unknown),
		strings.Join(accepted, ", "),
	)
}

func splitInternalRuntimeOverridePairs(pairs []paramPair, declaredNames map[string]struct{}) (userPairs []paramPair, internalPairs []paramPair) {
	for _, pair := range pairs {
		if isInternalRuntimeParam(pair.Name) && !isDeclaredRuntimeParam(declaredNames, pair.Name) {
			internalPairs = append(internalPairs, pair)
			continue
		}
		userPairs = append(userPairs, pair)
	}
	return userPairs, internalPairs
}

func appendInternalRuntimeEntries(entries []dagParamEntry, internalPairs []paramPair) []dagParamEntry {
	if len(internalPairs) == 0 {
		return entries
	}

	normalizedInternalPairs := appendInternalRuntimePairs(nil, internalPairs)
	result := make([]dagParamEntry, 0, len(entries)+len(normalizedInternalPairs))
	result = append(result, entries...)
	for _, pair := range normalizedInternalPairs {
		result = append(result, dagParamEntry{
			Name:     pair.Name,
			Value:    pair.Value,
			HasValue: true,
		})
	}
	return result
}

func appendInternalRuntimePairs(existing []paramPair, internalPairs []paramPair) []paramPair {
	if len(internalPairs) == 0 {
		return existing
	}

	result := append([]paramPair(nil), existing...)
	indexByName := make(map[string]int, len(result))
	for i, pair := range result {
		if pair.Name == "" {
			continue
		}
		indexByName[pair.Name] = i
	}

	for _, pair := range internalPairs {
		if pair.Name == "" {
			result = append(result, pair)
			continue
		}
		if idx, ok := indexByName[pair.Name]; ok {
			result[idx].Value = pair.Value
			continue
		}
		indexByName[pair.Name] = len(result)
		result = append(result, pair)
	}

	return result
}

func declaredRuntimeParamNames(entries []dagParamEntry) map[string]struct{} {
	if len(entries) == 0 {
		return nil
	}

	names := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if entry.Name == "" {
			continue
		}
		names[entry.Name] = struct{}{}
	}
	if len(names) == 0 {
		return nil
	}
	return names
}

func declaredRuntimeParamNamesForPlan(plan *dagParamPlan) map[string]struct{} {
	if len(plan.schemaProperties) == 0 {
		return declaredRuntimeParamNames(plan.entries)
	}

	names := make(map[string]struct{}, len(plan.schemaProperties))
	for name := range plan.schemaProperties {
		names[name] = struct{}{}
	}
	return names
}

func isDeclaredRuntimeParam(declaredNames map[string]struct{}, name string) bool {
	if len(declaredNames) == 0 {
		return false
	}
	_, ok := declaredNames[name]
	return ok
}

func isInternalRuntimeParam(name string) bool {
	switch name {
	case "WEBHOOK_PAYLOAD":
		return true
	case "WEBHOOK_HEADERS":
		return true
	default:
		return false
	}
}

func runtimePairsFromEntries(entries []dagParamEntry) []paramPair {
	pairs := make([]paramPair, 0, len(entries))
	for _, entry := range entries {
		if !entry.HasValue {
			continue
		}
		pairs = append(pairs, paramPair{Name: entry.Name, Value: entry.Value})
	}
	for i := range pairs {
		if pairs[i].Name == "" {
			pairs[i].Name = strconv.Itoa(i + 1)
		}
	}
	return pairs
}

func buildParamEvalScope(ctx BuildContext) *eval.EnvScope {
	if ctx.envScope != nil && ctx.envScope.scope != nil {
		return ctx.envScope.scope
	}

	scope := eval.NewEnvScope(nil, true)
	if len(ctx.opts.BuildEnv) > 0 {
		scope = scope.WithEntries(ctx.opts.BuildEnv, eval.EnvSourceDotEnv)
	}
	return scope
}

func resolveLegacyEntry(
	ctx BuildContext,
	entry *dagParamEntry,
	base dagParamEntry,
	overridden bool,
	scope **eval.EnvScope,
	index int,
) error {
	if overridden || strings.TrimSpace(base.Eval) == "" || ctx.opts.Has(BuildFlagNoEval) {
		addEntryToParamScope(scope, *entry, index)
		return nil
	}

	evalCtx := ctx.ctx
	if evalCtx == nil {
		evalCtx = context.Background()
	}
	if *scope != nil {
		evalCtx = eval.WithEnvScope(evalCtx, *scope)
	}

	value, err := eval.String(evalCtx, base.Eval, eval.WithOSExpansion())
	if err != nil {
		if base.HasValue {
			entry.Value = base.Value
			entry.HasValue = true
			addEntryToParamScope(scope, *entry, index)
			return nil
		}
		return core.NewValidationError(
			"params",
			base.Eval,
			fmt.Errorf("%w: parameter %q eval failed: %v", ErrInvalidParamValue, paramScopeName(base, index), err),
		)
	}

	entry.Value = value
	entry.HasValue = true
	addEntryToParamScope(scope, *entry, index)
	return nil
}

func addEntryToParamScope(scope **eval.EnvScope, entry dagParamEntry, index int) {
	if scope == nil || *scope == nil || !entry.HasValue {
		return
	}
	name := paramScopeName(entry, index)
	if name == "" {
		return
	}
	*scope = (*scope).WithEntry(name, entry.Value, eval.EnvSourceParam)
}

func paramScopeName(entry dagParamEntry, index int) string {
	if entry.Name != "" {
		return entry.Name
	}
	return strconv.Itoa(index + 1)
}

func normalizeTypedParamValue(value any, paramType string) (any, error) {
	switch paramType {
	case core.ParamDefTypeString:
		return stringifyUntypedValue(value), nil

	case core.ParamDefTypeInteger:
		switch v := value.(type) {
		case string:
			return coerceStringToType(v, paramType)
		default:
			number, err := toInt64(value)
			if err != nil {
				return nil, err
			}
			return number, nil
		}

	case core.ParamDefTypeNumber:
		switch v := value.(type) {
		case string:
			return coerceStringToType(v, paramType)
		default:
			number, err := toFloat64(value)
			if err != nil {
				return nil, err
			}
			return number, nil
		}

	case core.ParamDefTypeBoolean:
		switch v := value.(type) {
		case string:
			return coerceStringToType(v, paramType)
		case bool:
			return v, nil
		default:
			return nil, fmt.Errorf("expected a boolean")
		}

	default:
		return nil, fmt.Errorf("unsupported type %q", paramType)
	}
}

func coerceStringToType(value, paramType string) (any, error) {
	switch paramType {
	case core.ParamDefTypeString:
		return value, nil

	case core.ParamDefTypeInteger:
		number, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("cannot coerce %q to integer", value)
		}
		return number, nil

	case core.ParamDefTypeNumber:
		number, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil {
			return nil, fmt.Errorf("cannot coerce %q to number", value)
		}
		return number, nil

	case core.ParamDefTypeBoolean:
		switch {
		case strings.EqualFold(value, "true"):
			return true, nil
		case strings.EqualFold(value, "false"):
			return false, nil
		default:
			return nil, fmt.Errorf("cannot coerce %q to boolean", value)
		}

	default:
		return nil, fmt.Errorf("unsupported type %q", paramType)
	}
}

func toFloat64(value any) (float64, error) {
	switch v := value.(type) {
	case float64:
		return v, nil
	case float32:
		return float64(v), nil
	case int:
		return signedIntToFloat64(int64(v))
	case int8:
		return signedIntToFloat64(int64(v))
	case int16:
		return signedIntToFloat64(int64(v))
	case int32:
		return signedIntToFloat64(int64(v))
	case int64:
		return signedIntToFloat64(v)
	case uint:
		return unsignedIntToFloat64(uint64(v))
	case uint8:
		return unsignedIntToFloat64(uint64(v))
	case uint16:
		return unsignedIntToFloat64(uint64(v))
	case uint32:
		return unsignedIntToFloat64(uint64(v))
	case uint64:
		return unsignedIntToFloat64(v)
	default:
		return 0, fmt.Errorf("got %T", value)
	}
}

func signedIntToFloat64(value int64) (float64, error) {
	if value < -maxSafeFloat64Integer || value > maxSafeFloat64Integer {
		return 0, fmt.Errorf("integer exceeds float64 safe range")
	}
	return float64(value), nil
}

func unsignedIntToFloat64(value uint64) (float64, error) {
	if value > maxSafeFloat64Integer {
		return 0, fmt.Errorf("integer exceeds float64 safe range")
	}
	return float64(value), nil
}

func toInt(value any) (int, error) {
	number, err := toInt64(value)
	if err != nil {
		return 0, err
	}
	if number < -int64(maxIntValue)-1 || number > int64(maxIntValue) {
		return 0, fmt.Errorf("integer overflow")
	}
	return int(number), nil
}

func toInt64(value any) (int64, error) {
	switch v := value.(type) {
	case int:
		return int64(v), nil
	case int8:
		return int64(v), nil
	case int16:
		return int64(v), nil
	case int32:
		return int64(v), nil
	case int64:
		return v, nil
	case uint:
		return int64FromUint64(uint64(v))
	case uint8:
		return int64(v), nil
	case uint16:
		return int64(v), nil
	case uint32:
		return int64(v), nil
	case uint64:
		return int64FromUint64(v)
	case float32:
		if float32(int64(v)) != v {
			return 0, fmt.Errorf("expected an integer")
		}
		return int64(v), nil
	case float64:
		if float64(int64(v)) != v {
			return 0, fmt.Errorf("expected an integer")
		}
		return int64(v), nil
	default:
		return 0, fmt.Errorf("got %T", value)
	}
}

func int64FromUint64(value uint64) (int64, error) {
	if value > maxInt64AsUint {
		return 0, fmt.Errorf("integer overflow")
	}
	return int64(value), nil
}

func cloneParamEntries(entries []dagParamEntry) []dagParamEntry {
	if len(entries) == 0 {
		return nil
	}
	cloned := make([]dagParamEntry, len(entries))
	copy(cloned, entries)
	return cloned
}

func cloneParamDefs(defs []core.ParamDef) []core.ParamDef {
	if len(defs) == 0 {
		return nil
	}
	cloned := make([]core.ParamDef, len(defs))
	copy(cloned, defs)
	for i := range cloned {
		if len(cloned[i].Enum) > 0 {
			cloned[i].Enum = append([]any(nil), cloned[i].Enum...)
		}
	}
	return cloned
}

func containsTypedValue(values []any, target any) bool {
	for _, item := range values {
		if reflect.DeepEqual(item, target) {
			return true
		}
	}
	return false
}

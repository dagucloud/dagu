// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
)

// expandVariables expands variable references in the input string using the provided options.
func expandVariables(ctx context.Context, value string, opts *options) string {
	r := newResolver(ctx, opts)
	value = r.expandReferences(ctx, value)
	value = r.replaceVars(value)
	return value
}

// buildOptions creates options from the given functional options.
func buildOptions(opts []option) *options {
	options := newOptions()
	for _, opt := range opts {
		opt(options)
	}
	return options
}

// String substitutes environment variables and commands in the input string
// by running the default pipeline: quoted-refs, variables, substitute, shell-expand.
func evalString(ctx context.Context, input string, opts ...option) (string, error) {
	if input == "" {
		return "", nil
	}
	return defaultPipeline.execute(ctx, input, buildOptions(opts))
}

// intString evaluates the input string via String and converts the result to an integer.
func intString(ctx context.Context, input string, opts ...option) (int, error) {
	value, err := evalString(ctx, input, opts...)
	if err != nil {
		return 0, err
	}
	v, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("failed to convert %q to int: %w", value, err)
	}
	return v, nil
}

// stringFields recursively processes all string fields in a struct or map by expanding
// environment variables and substituting command outputs.
func stringFields[T any](ctx context.Context, obj T, opts ...option) (T, error) {
	v := reflect.ValueOf(obj)
	if v.Kind() != reflect.Struct && v.Kind() != reflect.Map {
		return obj, fmt.Errorf("input must be a struct or map, got %T", obj)
	}

	options := buildOptions(opts)
	transform := func(ctx context.Context, s string) (string, error) {
		return evalStringValue(ctx, s, options)
	}

	result, err := walkValue(ctx, v, transform)
	if err != nil {
		return obj, err
	}
	val, ok := result.Interface().(T)
	if !ok {
		return obj, fmt.Errorf("type assertion failed: expected %T, got %T", obj, result.Interface())
	}
	return val, nil
}

// expandReferences finds all ${NAME.path} references in the input string, resolves
// each NAME from dataMap as JSON, and replaces the placeholder with the extracted
// sub-path value via gojq. Unresolvable placeholders are left as-is.
func expandReferences(ctx context.Context, input string, dataMap map[string]string) string {
	return expandReferencesWithSteps(ctx, input, dataMap, nil)
}

// expandReferencesWithSteps is like expandReferences but also resolves step property
// references such as ${step_id.stdout}, ${step_id.stderr}, and ${step_id.exit_code}.
// OS environment is not expanded — only explicit dataMap entries and non-OS scope
// entries are used for resolution.
func expandReferencesWithSteps(ctx context.Context, input string, dataMap map[string]string, stepMap map[string]StepInfo) string {
	r := &resolver{
		variables: []map[string]string{dataMap},
		stepMap:   stepMap,
		scope:     GetEnvScope(ctx),
		expandOS:  false,
	}
	return r.expandReferences(ctx, input)
}

// Object recursively evaluates the string fields of the given object using variable
// expansion, command substitution, and env expansion. OS environment is NOT expanded
// by default — only explicitly provided vars and non-OS scope entries are used.
// This prevents OS variables like $HOME from being expanded in non-shell executor
// configs (SSH, Docker, S3, etc.) where they should be preserved for the remote env.
func object[T any](ctx context.Context, obj T, vars map[string]string, opts ...option) (T, error) {
	options := buildOptions(opts)
	options.Variables = append(options.Variables, vars)

	transform := func(ctx context.Context, s string) (string, error) {
		return evalStringValue(ctx, s, options)
	}

	v := reflect.ValueOf(obj)
	result, err := walkValue(ctx, v, transform)
	if err != nil {
		return obj, err
	}
	val, ok := result.Interface().(T)
	if !ok {
		return obj, fmt.Errorf("type assertion failed: expected %T, got %T", obj, result.Interface())
	}
	return val, nil
}

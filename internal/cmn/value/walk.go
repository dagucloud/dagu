// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import (
	"context"
	"reflect"
)

// stringTransform applies a transformation to a string value.
type stringTransform func(ctx context.Context, s string) (string, error)

// walkValue recursively walks a reflect.Value, applying transform to all string leaves.
// It handles strings, structs, maps, slices, pointers, and interfaces.
// Non-string leaf values are returned unchanged.
func walkValue(ctx context.Context, v reflect.Value, transform stringTransform) (reflect.Value, error) {
	for v.Kind() == reflect.Interface && !v.IsNil() {
		v = v.Elem()
	}

	//nolint:exhaustive
	switch v.Kind() {
	case reflect.String:
		s, err := transform(ctx, v.String())
		if err != nil {
			return v, err
		}
		return reflect.ValueOf(s), nil

	case reflect.Pointer:
		if v.IsNil() {
			return v, nil
		}
		walked, err := walkValue(ctx, v.Elem(), transform)
		if err != nil {
			return v, err
		}
		newPtr := reflect.New(v.Type().Elem())
		newPtr.Elem().Set(walked)
		return newPtr, nil

	case reflect.Struct:
		return walkStruct(ctx, v, transform)

	case reflect.Map:
		if v.IsNil() {
			return v, nil
		}
		return walkMap(ctx, v, transform)

	case reflect.Slice:
		if v.IsNil() {
			return v, nil
		}
		return walkSlice(ctx, v, transform)

	default:
		return v, nil
	}
}

// walkStruct creates a copy of the struct and walks each settable field.
func walkStruct(ctx context.Context, v reflect.Value, transform stringTransform) (reflect.Value, error) {
	result := reflect.New(v.Type()).Elem()
	result.Set(v)

	for _, field := range result.Fields() {
		if !field.CanSet() {
			continue
		}

		walked, err := walkValue(ctx, field, transform)
		if err != nil {
			return v, err
		}
		field.Set(walked)
	}
	return result, nil
}

// unwrapIndirections peels away interface and pointer wrappers from a reflect.Value,
// returning the underlying concrete value.
func unwrapIndirections(v reflect.Value) reflect.Value {
	for (v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer) && !v.IsNil() {
		v = v.Elem()
	}
	return v
}

// walkMap creates a new map and walks each value entry.
// Map values are fully unwrapped (both interfaces and pointers) before walking,
// so a map[string]any with *string values will produce string values in the output.
func walkMap(ctx context.Context, v reflect.Value, transform stringTransform) (reflect.Value, error) {
	newMap := reflect.MakeMap(v.Type())

	iter := v.MapRange()
	for iter.Next() {
		val := iter.Value()
		if v.Type().Elem().Kind() == reflect.Interface {
			val = unwrapIndirections(val)
		}
		walked, err := walkValue(ctx, val, transform)
		if err != nil {
			return v, err
		}
		newMap.SetMapIndex(iter.Key(), walked)
	}
	return newMap, nil
}

// walkSlice creates a new slice and walks each element.
func walkSlice(ctx context.Context, v reflect.Value, transform stringTransform) (reflect.Value, error) {
	newSlice := reflect.MakeSlice(v.Type(), v.Len(), v.Cap())

	for i := range v.Len() {
		walked, err := walkValue(ctx, v.Index(i), transform)
		if err != nil {
			return v, err
		}
		newSlice.Index(i).Set(walked)
	}
	return newSlice, nil
}

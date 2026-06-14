// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

// BuildConstsForTest exposes buildConsts to external tests.
func BuildConstsForTest(input any) (map[string]any, error) {
	return buildConsts(BuildContext{}, &dag{Consts: input})
}

// ValidateConstValueForTest exposes validateConstValue to external tests.
func ValidateConstValueForTest(key string, value any) (any, error) {
	return validateConstValue(key, value)
}

// ResolveConstListStringValueForTest exposes resolveConstListStringValue to external tests.
func ResolveConstListStringValueForTest(key string, value string, resolved map[string]any) (string, error) {
	return resolveConstListStringValue(key, value, resolved)
}

// FormatConstValueForTest exposes formatConstValue to external tests.
func FormatConstValueForTest(value any) string {
	return formatConstValue(value)
}

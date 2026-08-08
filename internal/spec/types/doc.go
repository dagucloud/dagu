// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package types provides typed union types for YAML fields that accept multiple formats.
//
// These types provide type-safe unmarshaling with early validation while maintaining
// full backward compatibility with existing YAML files.
//
// Design principles:
//  1. Each type captures all valid YAML representations for a field
//  2. Validation happens at unmarshal time, not at build time
//  3. Accessor methods provide type-safe access to the parsed values
//  4. IsZero() indicates whether the field was set in YAML
//
// The types in this package are designed to be used as drop-in replacements
// for `any` typed fields in the spec.definition struct, enabling gradual
// migration while maintaining backward compatibility.
package types

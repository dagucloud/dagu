// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agentrun

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dagucloud/dagu/v2/internal/cmn/jsonutil"
	"github.com/google/jsonschema-go/jsonschema"
)

// ResultReason is the reason that a typed result fails. Each reason is a
// failure of the kind error.
type ResultReason string

// Reasons of Spec 091.
const (
	// ReasonNoResult: no result message, or no structured output.
	ReasonNoResult ResultReason = "no_result"
	// ReasonInvalidJSON: the result is not valid JSON, or has a duplicate field.
	ReasonInvalidJSON ResultReason = "invalid_json"
	// ReasonSchemaMismatch: the value does not match the schema, or has an unknown field.
	ReasonSchemaMismatch ResultReason = "schema_mismatch"
)

// valueCutBytes is the size at which an error cuts the value that it names.
// messageCutBytes is the same for the message of the validator, which echoes the
// value that it rejects.
const (
	valueCutBytes   = 80
	messageCutBytes = 200
)

// ResultError is the failure of a typed result.
type ResultError struct {
	Reason ResultReason
	Err    error
}

// Error implements error.
func (e *ResultError) Error() string {
	return fmt.Sprintf("typed result: %s: %v", e.Reason, e.Err)
}

// Unwrap returns the cause.
func (e *ResultError) Unwrap() error { return e.Err }

// ResultSchema is the closed output_schema of an agent.run step, prepared to
// check a result.
type ResultSchema struct {
	resolved *jsonschema.Resolved
}

// NewResultSchema prepares a schema. It fails when the schema cannot be
// resolved.
func NewResultSchema(schema map[string]any) (*ResultSchema, error) {
	data, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal output_schema: %w", err)
	}
	var parsed jsonschema.Schema
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse output_schema: %w", err)
	}
	resolved, err := parsed.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	if err != nil {
		return nil, fmt.Errorf("failed to resolve output_schema: %w", err)
	}
	return &ResultSchema{resolved: resolved}, nil
}

// Check decodes a result with the strict decoder and validates it against the
// schema. It returns the result as one line of JSON with the keys in sorted
// order. An unknown field, a duplicate field and a value of the wrong type fail.
// The error names the key and the value, cut at 80 bytes.
func (s *ResultSchema) Check(raw []byte) ([]byte, *ResultError) {
	if len(raw) == 0 {
		return nil, &ResultError{Reason: ReasonNoResult, Err: errors.New("the agent gave no result")}
	}
	decoded, err := DecodeStrict(raw)
	if err != nil {
		return nil, &ResultError{Reason: ReasonInvalidJSON, Err: fmt.Errorf("%v (value: %s)", err, CutBytes(string(raw), valueCutBytes))}
	}
	object, ok := decoded.(map[string]any)
	if !ok {
		return nil, &ResultError{Reason: ReasonSchemaMismatch,
			Err: fmt.Errorf("the result must be a JSON object, got %T (value: %s)", decoded, CutBytes(string(raw), valueCutBytes))}
	}
	// The validator reads plain numbers, so it gets a plain decode of the value.
	var plain any
	if err := json.Unmarshal(raw, &plain); err != nil {
		return nil, &ResultError{Reason: ReasonInvalidJSON, Err: err}
	}
	if err := s.resolved.Validate(plain); err != nil {
		return nil, &ResultError{Reason: ReasonSchemaMismatch,
			Err: fmt.Errorf("%s (value: %s)", CutBytes(err.Error(), messageCutBytes), CutBytes(string(raw), valueCutBytes))}
	}
	line, err := jsonutil.MarshalUnescaped(object)
	if err != nil {
		return nil, &ResultError{Reason: ReasonInvalidJSON, Err: err}
	}
	return trimLine(line), nil
}

func trimLine(data []byte) []byte {
	for len(data) > 0 && (data[len(data)-1] == '\n' || data[len(data)-1] == '\r') {
		data = data[:len(data)-1]
	}
	return data
}

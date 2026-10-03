// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package agentstep holds what the built-in steps that drive a model
// themselves, such as browser, computer, and chat, share: with-block
// validation helpers, the respond tool, and lock upkeep.
package agentstep

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	llmpkg "github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/google/jsonschema-go/jsonschema"
)

var (
	// IdentifierPattern matches variable and ask.as names.
	IdentifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	// FileNamePattern matches screenshot and profile names.
	FileNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	// variableReferencePattern finds %name% references.
	variableReferencePattern = regexp.MustCompile(`%([A-Za-z_][A-Za-z0-9_]*)%`)
)

// VariableReferences returns the names a text references as %name%.
func VariableReferences(text string) []string {
	matches := variableReferencePattern.FindAllStringSubmatch(text, -1)
	names := make([]string, 0, len(matches))
	for _, match := range matches {
		names = append(names, match[1])
	}
	return names
}

// SubstituteVariables replaces %name% references with their values. Unknown
// names stay as written.
func SubstituteVariables(text string, values map[string]string) string {
	return variableReferencePattern.ReplaceAllStringFunc(text, func(match string) string {
		if value, ok := values[match[1:len(match)-1]]; ok {
			return value
		}
		return match
	})
}

// ValidateDuration checks a literal duration field. Values holding a
// reference resolve at run time and are not checked.
func ValidateDuration(field, value string) error {
	if value == "" || strings.Contains(value, "$") {
		return nil
	}
	if d, err := time.ParseDuration(value); err != nil || d <= 0 {
		return fmt.Errorf("%s %q must be a positive duration such as 30s", field, value)
	}
	return nil
}

// The schema library requires every node to be a distinct value, so shared
// shapes are built by functions.

// NoExtraProperties is the additionalProperties schema of a closed object.
func NoExtraProperties() *jsonschema.Schema {
	return &jsonschema.Schema{Not: &jsonschema.Schema{}}
}

// StringSchema accepts any string.
func StringSchema() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "string"}
}

// NonEmptyString accepts a string of at least one character.
func NonEmptyString() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "string", MinLength: new(1)}
}

// The respond tool is how a model returns a structured answer.
const (
	RespondToolName        = "respond"
	RespondToolDescription = "Return the answer as arguments that match the parameter schema exactly."
	// ToolChoiceRequired asks the model to call one of the offered tools.
	ToolChoiceRequired = "required"
)

// schemaKeysToStrip are schema annotations some providers reject in tool
// parameters.
var schemaKeysToStrip = []string{"$schema", "$id"}

// ToolParameters returns a response schema as respond tool parameters.
func ToolParameters(schema map[string]any) map[string]any {
	parameters := maps.Clone(schema)
	for _, key := range schemaKeysToStrip {
		delete(parameters, key)
	}
	return parameters
}

// StructuredAnswer returns the JSON the model produced, preferring the
// respond tool call and falling back to JSON in the text content.
func StructuredAnswer(resp *llmpkg.ChatResponse) (json.RawMessage, error) {
	for _, call := range resp.ToolCalls {
		if call.Function.Name == RespondToolName && json.Valid([]byte(call.Function.Arguments)) {
			return json.RawMessage(call.Function.Arguments), nil
		}
	}
	text := strings.TrimSpace(resp.Content)
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	text = strings.TrimSpace(text)
	if text != "" && json.Valid([]byte(text)) {
		return json.RawMessage(text), nil
	}
	return nil, errors.New("model did not return structured output")
}

// lockHeartbeatInterval keeps a held lock well inside dirlock's staleness
// threshold.
const lockHeartbeatInterval = 10 * time.Second

// KeepLockAlive refreshes a held lock until the returned function is called,
// so a long step does not lose it as stale.
func KeepLockAlive(ctx context.Context, lock dirlock.DirLock) context.CancelFunc {
	ctx, stop := context.WithCancel(context.WithoutCancel(ctx))
	go func() {
		ticker := time.NewTicker(lockHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = lock.Heartbeat(ctx)
			}
		}
	}()
	return stop
}

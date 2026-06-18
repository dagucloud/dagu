// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/dagucloud/dagu/internal/core"
	coreexec "github.com/dagucloud/dagu/internal/core/exec"
)

var outputFileDelimiterPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func readDeclaredStepOutputs(ctx context.Context, path string, declarations []core.StepOutputDeclaration) (map[string]string, error) {
	data, err := readStepOutputFile(path, maxOutputSize(ctx))
	if err != nil {
		return nil, err
	}
	return parseDeclaredStepOutputs(data, declarations)
}

func readStepOutputFile(path string, limit int64) ([]byte, error) {
	// #nosec G304 -- DAGU_OUTPUT_FILE is created by the runtime for the current step attempt.
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("step outputs exceeded maximum size limit of %d bytes", limit)
	}
	return data, nil
}

func parseDeclaredStepOutputs(data []byte, declarations []core.StepOutputDeclaration) (map[string]string, error) {
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("step output file is not valid UTF-8")
	}

	declared := make(map[string]core.StepOutputDeclaration, len(declarations))
	for _, declaration := range declarations {
		declared[declaration.Name] = declaration
	}

	text := normalizeOutputFileLineEndings(string(data))
	values := make(map[string]string, len(declarations))
	if text == "" {
		if len(declarations) == 0 {
			return values, nil
		}
		return nil, missingDeclaredOutputError(declarations, values)
	}

	lines := strings.Split(text, "\n")
	if len(lines) > 1 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	for idx := 0; idx < len(lines); idx++ {
		line := lines[idx]
		if line == "" {
			return nil, fmt.Errorf("empty line at line %d is not a valid step output record", idx+1)
		}

		equalsAt := strings.Index(line, "=")
		heredocAt := strings.Index(line, "<<")
		if heredocAt >= 0 && (equalsAt < 0 || heredocAt < equalsAt) {
			name := line[:heredocAt]
			delimiter := line[heredocAt+len("<<"):]
			if name == "" {
				return nil, fmt.Errorf("missing output name at line %d", idx+1)
			}
			if delimiter == "" || !outputFileDelimiterPattern.MatchString(delimiter) {
				return nil, fmt.Errorf("invalid multiline delimiter at line %d", idx+1)
			}
			end := idx + 1
			for end < len(lines) && lines[end] != delimiter {
				end++
			}
			if end >= len(lines) {
				return nil, fmt.Errorf("unclosed multiline output %q", name)
			}
			if err := recordDeclaredStepOutput(values, declared, name, strings.Join(lines[idx+1:end], "\n")); err != nil {
				return nil, err
			}
			idx = end
			continue
		}

		if equalsAt < 0 {
			return nil, fmt.Errorf("invalid step output record at line %d", idx+1)
		}
		name := line[:equalsAt]
		value := line[equalsAt+1:]
		if name == "" {
			return nil, fmt.Errorf("missing output name at line %d", idx+1)
		}
		if err := recordDeclaredStepOutput(values, declared, name, value); err != nil {
			return nil, err
		}
	}

	if len(values) != len(declarations) {
		return nil, missingDeclaredOutputError(declarations, values)
	}
	return values, nil
}

func normalizeOutputFileLineEndings(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	return strings.ReplaceAll(value, "\r", "\n")
}

func recordDeclaredStepOutput(
	values map[string]string,
	declared map[string]core.StepOutputDeclaration,
	name string,
	value string,
) error {
	declaration, ok := declared[name]
	if !ok {
		return fmt.Errorf("undeclared step output %q", name)
	}
	if _, exists := values[name]; exists {
		return fmt.Errorf("duplicate step output %q", name)
	}
	if declaration.Type == core.StepDeclaredOutputTypeJSON && !json.Valid([]byte(value)) {
		return fmt.Errorf("step output %q is not valid JSON", name)
	}
	values[name] = value
	return nil
}

func missingDeclaredOutputError(declarations []core.StepOutputDeclaration, values map[string]string) error {
	for _, declaration := range declarations {
		if _, ok := values[declaration.Name]; !ok {
			return fmt.Errorf("declared step output %q was not emitted", declaration.Name)
		}
	}
	return fmt.Errorf("declared step outputs were not emitted")
}

func serializeDeclaredStepOutputs(ctx context.Context, values map[string]string) (string, error) {
	data, err := json.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("failed to serialize step outputs: %w", err)
	}
	if int64(len(data)) > maxOutputSize(ctx) {
		return "", fmt.Errorf("step outputs exceeded maximum size limit of %d bytes", maxOutputSize(ctx))
	}
	return string(data), nil
}

func (n *Node) captureDeclaredStepOutputs(ctx context.Context) error {
	path := n.stepOutputFile()
	if path == "" {
		return fmt.Errorf("%s was not set", coreexec.EnvKeyDAGUOutputFile)
	}

	values, err := readDeclaredStepOutputs(ctx, path, n.Step().Outputs)
	if err != nil {
		return fmt.Errorf("failed to parse %s: %w", coreexec.EnvKeyDAGUOutputFile, err)
	}
	if len(values) == 0 {
		n.clearStepOutputsValue()
		return nil
	}

	serialized, err := serializeDeclaredStepOutputs(ctx, values)
	if err != nil {
		return err
	}
	n.setStepOutputsValue(serialized)
	return nil
}

// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/workspace"
	"github.com/goccy/go-yaml"
	"github.com/spf13/cobra"
)

// Validate creates the 'validate' CLI command that checks a DAG spec for errors.
//
// It follows the same validation logic used by the API's UpdateDAGSpec handler:
// - Load the YAML without evaluation
// - Run DAG.Validate()
//
// The command prints validation results and any errors found.
// Unlike other commands, this does NOT use NewCommand wrapper to allow proper
// error handling in tests without requiring subprocess patterns.
func Validate() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate [flags] <DAG definition>",
		Short: "Validate a DAG specification",
		Long: `Validate a DAG YAML file without executing it.

Prints a human-readable result instead of structured logs.
Checks structural correctness and references (e.g., step dependencies)
similar to the server-side spec validation.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := NewContext(cmd, nil)
			if err != nil {
				return fmt.Errorf("initialization error: %w", err)
			}
			return runValidate(ctx, args)
		},
	}

	// Initialize flags required by NewContext
	initFlags(cmd)

	return cmd
}

func runValidate(ctx *Context, args []string) error {
	// Try loading the DAG without evaluation, resolving relative names against DAGsDir
	loadOpts := []spec.LoadOption{
		spec.WithoutEval(),
		spec.WithDAGsDir(ctx.Config.Paths.DAGsDir),
		spec.WithWorkspaceBaseConfigDir(workspace.BaseConfigDir(ctx.Config.Paths.DAGsDir)),
	}
	if ctx.Config.Paths.BaseConfig != "" {
		loadOpts = append(loadOpts, spec.WithBaseConfig(ctx.Config.Paths.BaseConfig))
	}

	dag, err := spec.Load(ctx, args[0], loadOpts...)

	if err != nil {
		// Collect and return a formatted error message
		return errors.New(formatValidationErrors(args[0], err))
	}

	// Run additional DAG-level validation (e.g., dependency references)
	if vErr := dag.Validate(); vErr != nil {
		return errors.New(formatValidationErrors(args[0], vErr))
	}

	for _, warning := range collectDeprecatedSyntaxWarnings(args[0]) {
		logger.Warn(ctx, warning, tag.File(args[0]))
	}

	// Success
	logger.Info(ctx, "DAG spec is valid",
		tag.File(args[0]),
		tag.Name(dag.GetName()),
	)
	return nil
}

func collectDeprecatedSyntaxWarnings(file string) []string {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil
	}

	var warnings []string
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	docIndex := 0
	for {
		var doc map[string]any
		if err := decoder.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil
		}
		if len(doc) == 0 {
			docIndex++
			continue
		}
		prefix := ""
		if docIndex > 0 {
			prefix = fmt.Sprintf("document[%d].", docIndex)
		}
		warnings = append(warnings, deprecatedSyntaxWarningsForDocument(prefix, doc)...)
		docIndex++
	}
	return warnings
}

func deprecatedSyntaxWarningsForDocument(prefix string, doc map[string]any) []string {
	var warnings []string
	if _, ok := doc["step_types"]; ok {
		warnings = append(warnings, fmt.Sprintf("Deprecated DAG syntax: %sstep_types is deprecated; use actions", prefix))
	}
	warnings = append(warnings, deprecatedSyntaxWarningsForSteps(prefix+"steps", doc["steps"])...)
	if handlerRaw, ok := doc["handler_on"].(map[string]any); ok {
		for name, raw := range handlerRaw {
			warnings = append(warnings, deprecatedSyntaxWarningsForStep(fmt.Sprintf("%shandler_on.%s", prefix, name), raw)...)
		}
	}
	return warnings
}

func deprecatedSyntaxWarningsForSteps(path string, raw any) []string {
	switch steps := raw.(type) {
	case []any:
		warnings := make([]string, 0)
		for i, stepRaw := range steps {
			warnings = append(warnings, deprecatedSyntaxWarningsForStep(fmt.Sprintf("%s[%d]", path, i), stepRaw)...)
		}
		return warnings
	case map[string]any:
		warnings := make([]string, 0)
		for name, stepRaw := range steps {
			warnings = append(warnings, deprecatedSyntaxWarningsForStep(fmt.Sprintf("%s.%s", path, name), stepRaw)...)
		}
		return warnings
	default:
		return nil
	}
}

func deprecatedSyntaxWarningsForStep(path string, raw any) []string {
	switch step := raw.(type) {
	case string:
		return []string{fmt.Sprintf("Deprecated DAG syntax: %s string shorthand is deprecated; use run", path)}
	case map[string]any:
		return deprecatedSyntaxWarningsForStepMap(path, step)
	default:
		return nil
	}
}

func deprecatedSyntaxWarningsForStepMap(path string, step map[string]any) []string {
	replacements := map[string]string{
		"agent":          "use action: agent.run with with",
		"call":           "use action: dag.run with with.dag",
		"command":        "use run",
		"config":         "use with",
		"exec":           "use action: exec",
		"llm":            "use action: chat.completion with with",
		"messages":       "use action: chat.completion or action: agent.run",
		"params":         "use action: dag.run with with.params",
		"routes":         "use action: router.route with with.routes",
		"script":         "use run",
		"shell":          "use run with with.shell",
		"shell_args":     "use run with with.shell_args",
		"shell_packages": "use run with with.shell_packages",
		"type":           "use action",
		"value":          "use action: router.route with with.value",
	}

	hasRun := false
	if _, ok := step["run"]; ok {
		hasRun = true
	}
	hasAction := false
	if _, ok := step["action"]; ok {
		hasAction = true
	}

	var warnings []string
	for field, replacement := range replacements {
		if _, ok := step[field]; ok {
			warnings = append(warnings, fmt.Sprintf("Deprecated DAG syntax: %s.%s is deprecated; %s", path, field, replacement))
		}
	}
	if _, ok := step["with"]; ok && !hasRun && !hasAction {
		warnings = append(warnings, fmt.Sprintf("Deprecated DAG syntax: %s.with is deprecated with legacy execution syntax; use action with with", path))
	}
	return warnings
}

// formatValidationErrors builds a readable error output from a (possibly wrapped) error.
func formatValidationErrors(file string, err error) string {
	// Collect message strings
	var msgs []string
	var list core.ErrorList
	if errors.As(err, &list) {
		msgs = list.ToStringList()
	} else {
		msgs = []string{err.Error()}
	}

	// Build readable, consistent output: one bullet per error, and if an
	// error spans multiple lines, indent subsequent lines for readability.
	var sb strings.Builder
	fmt.Fprintf(&sb, "Validation failed for %s\n", file)
	for _, m := range msgs {
		lines := strings.Split(strings.TrimRight(m, "\n"), "\n")
		for i, line := range lines {
			if strings.TrimSpace(line) == "" {
				continue
			}
			if i == 0 {
				sb.WriteString("- ")
			} else {
				sb.WriteString("  ") // indent continuation lines of the same error
			}
			sb.WriteString(line)
			sb.WriteByte('\n')
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

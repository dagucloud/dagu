// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	cmnvalue "github.com/dagucloud/dagu/internal/cmn/value"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/workspace"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
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
		Use:          "validate [flags] <DAG definition>",
		Short:        "Validate a DAG specification",
		SilenceUsage: true,
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
	validatedInput, err := validateWorkflowFile(args[0])
	if err != nil {
		return errors.New(formatValidationErrors(args[0], err))
	}

	// Try loading the DAG without evaluation, resolving relative names against DAGsDir
	loadOpts := []spec.LoadOption{
		spec.WithoutEval(),
		spec.WithDAGsDir(ctx.Config.Paths.DAGsDir),
		spec.WithWorkspaceBaseConfigDir(workspace.BaseConfigDir(ctx.Config.Paths.DAGsDir)),
	}
	if ctx.Config.Paths.BaseConfig != "" {
		loadOpts = append(loadOpts, spec.WithBaseConfig(ctx.Config.Paths.BaseConfig))
	}

	loadResult, err := spec.LoadWithResult(ctx, args[0], loadOpts...)
	if err != nil {
		// Collect and return a formatted error message
		return errors.New(formatValidationErrors(args[0], err))
	}
	dag := loadResult.DAG

	if !validatedInput && dag.Location != "" {
		if _, err := validateWorkflowFile(dag.Location); err != nil {
			return errors.New(formatValidationErrors(dag.Location, err))
		}
	}

	// Run additional DAG-level validation (e.g., dependency references)
	if vErr := dag.Validate(); vErr != nil {
		return errors.New(formatValidationErrors(args[0], vErr))
	}

	logValidationWarnings(ctx, args[0], append(dag.BuildWarnings, collectDeprecatedSyntaxWarnings(dag)...))
	logValueReferenceNotices(ctx, args[0], loadResult.ValueReferenceNotices)

	return nil
}

func logValueReferenceNotices(ctx *Context, file string, notices []cmnvalue.ValueReferenceNotice) {
	for _, notice := range notices {
		if notice.Message == "" {
			continue
		}
		logger.Info(ctx, notice.Message, tag.File(file))
	}
}

func logValidationWarnings(ctx *Context, file string, warnings []string) {
	seen := make(map[string]struct{}, len(warnings))
	for _, warning := range warnings {
		if warning == "" {
			continue
		}
		if _, ok := seen[warning]; ok {
			continue
		}
		seen[warning] = struct{}{}
		logger.Warn(ctx, warning, tag.File(file))
	}
}

func validateWorkflowFile(path string) (bool, error) {
	data, err := os.ReadFile(path) //nolint:gosec // validate reads the CLI-provided workflow file path.
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, validateWorkflowData(data)
}

func validateWorkflowData(data []byte) error {
	file, err := parser.ParseBytes(data, 0)
	if err != nil {
		return err
	}
	if file == nil || len(file.Docs) == 0 {
		return errors.New("yaml stream must contain at least one DAG document")
	}

	var errs workflowValidationErrors
	names := map[string]struct{}{}
	for i, doc := range file.Docs {
		errs.add(validateWorkflowDocument(i, doc, names)...)
	}
	if len(errs) > 0 {
		return errs
	}
	return nil
}

func validateWorkflowDocument(index int, doc *ast.DocumentNode, names map[string]struct{}) []error {
	var errs workflowValidationErrors
	label := validateDocumentLabel(index)

	root, err := validateDocumentRoot(label, doc)
	if err != nil {
		return []error{err}
	}

	fields := collectValidateRootFields(root)
	errs.add(validateDocumentName(index, label, fields, names)...)
	errs.add(validateDocumentSteps(label, fields)...)
	return errs
}

func validateDocumentRoot(label string, doc *ast.DocumentNode) (*ast.MappingNode, error) {
	if doc == nil || doc.Body == nil {
		return nil, fmt.Errorf("%s must not be empty", label)
	}

	root, ok := doc.Body.(*ast.MappingNode)
	if !ok || root == nil || len(root.Values) == 0 {
		return nil, fmt.Errorf("%s must not be empty", label)
	}

	return root, nil
}

func collectValidateRootFields(root *ast.MappingNode) map[string]*ast.MappingValueNode {
	fields := map[string]*ast.MappingValueNode{}
	for _, item := range root.Values {
		key, ok := validateScalarString(item.Key)
		if ok {
			fields[key] = item
		}
	}
	return fields
}

func validateDocumentName(index int, label string, fields map[string]*ast.MappingValueNode, names map[string]struct{}) []error {
	var errs workflowValidationErrors
	if _, ok := fields["name"]; index == 0 && ok {
		errs.add(fmt.Errorf("%s must not define name", label))
	}
	if index > 0 {
		nameField, ok := fields["name"]
		if !ok {
			errs.add(fmt.Errorf("%s must define name", label))
		} else if name, ok := validateScalarString(nameField.Value); !ok || strings.TrimSpace(name) == "" {
			errs.add(fmt.Errorf("%s name must be a non-empty string", label))
		} else if _, exists := names[name]; exists {
			errs.add(fmt.Errorf("DAG document name %q must be unique", name))
		} else {
			names[name] = struct{}{}
		}
	}
	return errs
}

func validateDocumentSteps(label string, fields map[string]*ast.MappingValueNode) []error {
	stepsField, ok := fields["steps"]
	if !ok {
		return []error{fmt.Errorf("%s must define steps", label)}
	}

	steps, ok := stepsField.Value.(*ast.SequenceNode)
	if !ok || steps == nil || len(steps.Values) == 0 {
		return []error{fmt.Errorf("%s steps must be a non-empty sequence", label)}
	}
	return nil
}

func validateDocumentLabel(index int) string {
	if index == 0 {
		return "entrypoint document"
	}
	return fmt.Sprintf("document %d", index+1)
}

func validateScalarString(node ast.Node) (string, bool) {
	switch n := node.(type) {
	case *ast.StringNode:
		return n.Value, true
	default:
		return "", false
	}
}

type workflowValidationErrors []error

func (e *workflowValidationErrors) add(errs ...error) {
	for _, err := range errs {
		if err != nil {
			*e = append(*e, err)
		}
	}
}

func (e workflowValidationErrors) Error() string {
	var b strings.Builder
	for i, err := range e {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(err.Error())
	}
	return b.String()
}

func collectDeprecatedSyntaxWarnings(dag *core.DAG) []string {
	if dag == nil {
		return nil
	}

	warnings := spec.DeprecatedSyntaxWarnings(dag.YamlData)
	if len(dag.LocalDAGs) == 0 {
		return warnings
	}

	names := make([]string, 0, len(dag.LocalDAGs))
	for name := range dag.LocalDAGs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		warnings = append(warnings, spec.DeprecatedSyntaxWarnings(dag.LocalDAGs[name].YamlData)...)
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

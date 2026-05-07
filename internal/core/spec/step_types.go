// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"strconv"
	"strings"
	"sync"
	gotemplate "text/template"

	"github.com/dagucloud/dagu/internal/cmn/templatefuncs"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/spec/types"
	"github.com/goccy/go-yaml"
	"github.com/google/jsonschema-go/jsonschema"
)

type customStepTypeSpec struct {
	Type        string         `yaml:"type,omitempty"`
	Description string         `yaml:"description,omitempty"`
	InputSchema any            `yaml:"input_schema,omitempty"`
	Template    map[string]any `yaml:"template,omitempty"`
}

type customStepType struct {
	Name        string
	Type        string
	Kind        customStepKind
	Description string
	InputSchema *jsonschema.Resolved
	Template    map[string]any
}

type customStepKind string

const (
	customStepKindStepType customStepKind = "step_type"
	customStepKindAction   customStepKind = "action"
)

type customStepTypeRegistry struct {
	entries map[string]*customStepType
}

func (r *customStepTypeRegistry) Lookup(name string) (*customStepType, bool) {
	if r == nil {
		return nil, false
	}
	def, ok := r.entries[strings.TrimSpace(name)]
	return def, ok
}

var customStepTypeNameRegexp = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)

var customActionNameRegexp = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*(\.[A-Za-z][A-Za-z0-9_-]*)*$`)

var customStepRuntimeExpressionRegexp = regexp.MustCompile("`[^`]+`|\\$\\{[^}]+\\}|\\$[A-Za-z_][A-Za-z0-9_]*")

var customStepWholeRuntimeExpressionRegexp = regexp.MustCompile("^\\s*(?:`[^`]+`|\\$\\{[^}]+\\}|\\$[A-Za-z_][A-Za-z0-9_]*)\\s*$")

var builtinStepTypeNames = map[string]struct{}{
	"agent":         {},
	"archive":       {},
	"chat":          {},
	"command":       {},
	"container":     {},
	"dag":           {},
	"docker":        {},
	"gha":           {},
	"github-action": {},
	"github_action": {},
	"harness":       {},
	"http":          {},
	"jq":            {},
	"k8s":           {},
	"kubernetes":    {},
	"log":           {},
	"mail":          {},
	"noop":          {},
	"parallel":      {},
	"postgres":      {},
	"redis":         {},
	"router":        {},
	"s3":            {},
	"sftp":          {},
	"shell":         {},
	"sqlite":        {},
	"ssh":           {},
	"subworkflow":   {},
	"template":      {},
}

var registeredExecutorTypeNames = map[string]struct{}{}

var stepTypeNamesMu sync.RWMutex

// IsValidExecutorTypeName reports whether name is valid for an executor type.
func IsValidExecutorTypeName(name string) bool {
	return customStepTypeNameRegexp.MatchString(strings.TrimSpace(name))
}

// RegisterExecutorTypeName registers a runtime executor type name so DAG
// loading accepts steps that use it directly in the type field.
func RegisterExecutorTypeName(name string) {
	name = strings.TrimSpace(name)
	if !IsValidExecutorTypeName(name) {
		return
	}
	stepTypeNamesMu.Lock()
	defer stepTypeNamesMu.Unlock()
	if _, builtin := builtinStepTypeNames[name]; !builtin {
		registeredExecutorTypeNames[name] = struct{}{}
	}
	builtinStepTypeNames[name] = struct{}{}
}

// UnregisterExecutorTypeName removes a runtime executor type name that was
// registered by RegisterExecutorTypeName. Built-in names are retained.
func UnregisterExecutorTypeName(name string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	stepTypeNamesMu.Lock()
	defer stepTypeNamesMu.Unlock()
	if _, registered := registeredExecutorTypeNames[name]; !registered {
		return
	}
	delete(registeredExecutorTypeNames, name)
	delete(builtinStepTypeNames, name)
}

var customStepForbiddenCallSiteFields = map[string]struct{}{
	"agent":          {},
	"call":           {},
	"command":        {},
	"container":      {},
	"exec":           {},
	"llm":            {},
	"messages":       {},
	"parallel":       {},
	"params":         {},
	"routes":         {},
	"script":         {},
	"shell":          {},
	"shell_args":     {},
	"shell_packages": {},
	"value":          {},
	"working_dir":    {},
}

func buildCustomStepActionRegistry(
	baseStepTypes, localStepTypes map[string]customStepTypeSpec,
	baseActions, localActions map[string]customStepTypeSpec,
) (*customStepTypeRegistry, error) {
	if len(baseStepTypes) == 0 && len(localStepTypes) == 0 && len(baseActions) == 0 && len(localActions) == 0 {
		return nil, nil
	}

	registry := &customStepTypeRegistry{
		entries: make(map[string]*customStepType, len(baseStepTypes)+len(localStepTypes)+len(baseActions)+len(localActions)),
	}
	if err := addCustomStepTypeDefinitions(registry, baseStepTypes, "base config"); err != nil {
		return nil, err
	}
	if err := addCustomActionDefinitions(registry, baseActions, "base config"); err != nil {
		return nil, err
	}
	if err := addCustomStepTypeDefinitions(registry, localStepTypes, "DAG"); err != nil {
		return nil, err
	}
	if err := addCustomActionDefinitions(registry, localActions, "DAG"); err != nil {
		return nil, err
	}
	return registry, nil
}

func buildCustomStepTypeRegistry(base, local map[string]customStepTypeSpec) (*customStepTypeRegistry, error) {
	if len(base) == 0 && len(local) == 0 {
		return nil, nil
	}

	registry := &customStepTypeRegistry{
		entries: make(map[string]*customStepType, len(base)+len(local)),
	}

	for name, spec := range base {
		normalizedName := strings.TrimSpace(name)
		if _, exists := registry.entries[normalizedName]; exists {
			return nil, core.NewValidationError(
				fmt.Sprintf("step_types.%s", normalizedName),
				normalizedName,
				fmt.Errorf("duplicate custom step type %q is defined in base config", normalizedName),
			)
		}
		def, err := validateCustomStepTypeSpec(name, spec)
		if err != nil {
			return nil, err
		}
		registry.entries[normalizedName] = def
	}

	for name, spec := range local {
		normalizedName := strings.TrimSpace(name)
		if _, exists := registry.entries[normalizedName]; exists {
			return nil, core.NewValidationError(
				fmt.Sprintf("step_types.%s", normalizedName),
				normalizedName,
				fmt.Errorf("duplicate custom step type %q is defined in both base config and DAG", normalizedName),
			)
		}
		def, err := validateCustomStepTypeSpec(name, spec)
		if err != nil {
			return nil, err
		}
		registry.entries[normalizedName] = def
	}

	return registry, nil
}

func addCustomStepTypeDefinitions(registry *customStepTypeRegistry, defs map[string]customStepTypeSpec, scope string) error {
	for name, spec := range defs {
		normalizedName := strings.TrimSpace(name)
		if existing, exists := registry.entries[normalizedName]; exists {
			return duplicateCustomDefinitionError("step_types", normalizedName, existing, scope)
		}
		def, err := validateCustomStepTypeSpec(name, spec)
		if err != nil {
			return err
		}
		registry.entries[normalizedName] = def
	}
	return nil
}

func addCustomActionDefinitions(registry *customStepTypeRegistry, defs map[string]customStepTypeSpec, scope string) error {
	for name, spec := range defs {
		normalizedName := strings.TrimSpace(name)
		if existing, exists := registry.entries[normalizedName]; exists {
			return duplicateCustomDefinitionError("actions", normalizedName, existing, scope)
		}
		def, err := validateCustomActionSpec(name, spec)
		if err != nil {
			return err
		}
		registry.entries[normalizedName] = def
	}
	return nil
}

func duplicateCustomDefinitionError(field, name string, existing *customStepType, scope string) error {
	if existing != nil && existing.Kind == customStepKindAction {
		return core.NewValidationError(
			fmt.Sprintf("%s.%s", field, name),
			name,
			fmt.Errorf("duplicate custom action %q conflicts with an existing custom action or step type in %s", name, scope),
		)
	}
	return core.NewValidationError(
		fmt.Sprintf("%s.%s", field, name),
		name,
		fmt.Errorf("duplicate custom step type %q is defined in %s", name, scope),
	)
}

func expandedCustomStepExecutorType(targetType string, rendered map[string]any) string {
	targetType = strings.TrimSpace(targetType)
	switch targetType {
	case "":
		return ""
	case "command", "shell":
		// Preserve the implicit command executor for ordinary command/script
		// templates so DAG-level container/ssh/redis/harness inference matches
		// plain command steps. Keep explicit command/shell typing for exec
		// templates because exec is defined as a direct-command form.
		if _, hasExec := rendered["exec"]; !hasExec {
			return ""
		}
		return targetType
	default:
		return targetType
	}
}

func validateCustomStepTypeSpec(name string, spec customStepTypeSpec) (*customStepType, error) {
	name = strings.TrimSpace(name)
	if !customStepTypeNameRegexp.MatchString(name) {
		return nil, core.NewValidationError(
			fmt.Sprintf("step_types.%s", name),
			name,
			fmt.Errorf("custom step type names must match %s", customStepTypeNameRegexp.String()),
		)
	}
	if isBuiltinStepTypeName(name) {
		return nil, core.NewValidationError(
			fmt.Sprintf("step_types.%s", name),
			name,
			fmt.Errorf("custom step type name %q conflicts with a builtin step type", name),
		)
	}

	targetType := strings.TrimSpace(spec.Type)
	if targetType == "" {
		return nil, core.NewValidationError(
			fmt.Sprintf("step_types.%s.type", name),
			spec.Type,
			fmt.Errorf("type is required"),
		)
	}
	if !isBuiltinStepTypeName(targetType) {
		return nil, core.NewValidationError(
			fmt.Sprintf("step_types.%s.type", name),
			spec.Type,
			fmt.Errorf("unknown builtin step type %q", targetType),
		)
	}
	if spec.InputSchema == nil {
		return nil, core.NewValidationError(
			fmt.Sprintf("step_types.%s.input_schema", name),
			nil,
			fmt.Errorf("input_schema is required"),
		)
	}
	if len(spec.Template) == 0 {
		return nil, core.NewValidationError(
			fmt.Sprintf("step_types.%s.template", name),
			spec.Template,
			fmt.Errorf("template is required"),
		)
	}
	if _, exists := spec.Template["type"]; exists {
		return nil, core.NewValidationError(
			fmt.Sprintf("step_types.%s.template.type", name),
			spec.Template["type"],
			fmt.Errorf("template.type is not allowed; use step_types.%s.type instead", name),
		)
	}

	inputSchema, err := resolveCustomStepTypeInputSchema(name, spec.InputSchema)
	if err != nil {
		return nil, err
	}

	return &customStepType{
		Name:        name,
		Type:        targetType,
		Kind:        customStepKindStepType,
		Description: strings.TrimSpace(spec.Description),
		InputSchema: inputSchema,
		Template:    cloneMap(spec.Template),
	}, nil
}

func validateCustomActionSpec(name string, spec customStepTypeSpec) (*customStepType, error) {
	name = strings.TrimSpace(name)
	if !customActionNameRegexp.MatchString(name) {
		return nil, core.NewValidationError(
			fmt.Sprintf("actions.%s", name),
			name,
			fmt.Errorf("custom action names must match %s", customActionNameRegexp.String()),
		)
	}
	if isBuiltinActionName(name) {
		return nil, core.NewValidationError(
			fmt.Sprintf("actions.%s", name),
			name,
			fmt.Errorf("custom action name %q conflicts with a builtin action", name),
		)
	}
	if strings.TrimSpace(spec.Type) != "" {
		return nil, core.NewValidationError(
			fmt.Sprintf("actions.%s.type", name),
			spec.Type,
			fmt.Errorf("type is not supported for actions; put run or action in actions.%s.template", name),
		)
	}
	if spec.InputSchema == nil {
		return nil, core.NewValidationError(
			fmt.Sprintf("actions.%s.input_schema", name),
			nil,
			fmt.Errorf("input_schema is required"),
		)
	}
	if len(spec.Template) == 0 {
		return nil, core.NewValidationError(
			fmt.Sprintf("actions.%s.template", name),
			spec.Template,
			fmt.Errorf("template is required"),
		)
	}
	_, hasRun := spec.Template["run"]
	_, hasAction := spec.Template["action"]
	if hasRun == hasAction {
		return nil, core.NewValidationError(
			fmt.Sprintf("actions.%s.template", name),
			spec.Template,
			fmt.Errorf("custom action template must define exactly one of run or action"),
		)
	}

	inputSchema, err := resolveCustomActionInputSchema(name, spec.InputSchema)
	if err != nil {
		return nil, err
	}

	return &customStepType{
		Name:        name,
		Kind:        customStepKindAction,
		Description: strings.TrimSpace(spec.Description),
		InputSchema: inputSchema,
		Template:    cloneMap(spec.Template),
	}, nil
}

func resolveCustomStepTypeInputSchema(name string, schemaDecl any) (*jsonschema.Resolved, error) {
	return resolveCustomInputSchema(fmt.Sprintf("step_types.%s.input_schema", name), schemaDecl)
}

func resolveCustomActionInputSchema(name string, schemaDecl any) (*jsonschema.Resolved, error) {
	return resolveCustomInputSchema(fmt.Sprintf("actions.%s.input_schema", name), schemaDecl)
}

func resolveCustomInputSchema(field string, schemaDecl any) (*jsonschema.Resolved, error) {
	schemaMap, ok := schemaDecl.(map[string]any)
	if !ok {
		return nil, core.NewValidationError(
			field,
			schemaDecl,
			fmt.Errorf("input_schema must be an inline JSON Schema object"),
		)
	}
	resolved, err := resolveSchemaDeclaration(schemaMap, "", "")
	if err != nil {
		return nil, core.NewValidationError(
			field,
			schemaDecl,
			err,
		)
	}
	root := resolved.Schema()
	if root == nil || !schemaDeclaresObject(root) {
		return nil, core.NewValidationError(
			field,
			schemaDecl,
			fmt.Errorf("input_schema must resolve to an object schema"),
		)
	}
	return resolved, nil
}

func schemaDeclaresObject(root *jsonschema.Schema) bool {
	if root == nil {
		return false
	}
	if root.Type == "object" {
		return true
	}
	return len(root.Types) == 1 && root.Types[0] == "object"
}

func isBuiltinStepTypeName(name string) bool {
	stepTypeNamesMu.RLock()
	defer stepTypeNamesMu.RUnlock()
	_, ok := builtinStepTypeNames[strings.TrimSpace(name)]
	return ok
}

func validateCustomStepInput(stepTypeName string, schema *jsonschema.Resolved, fieldName string, input map[string]any) (map[string]any, error) {
	working := make(map[string]any, len(input))
	maps.Copy(working, input)
	if err := schema.ApplyDefaults(&working); err != nil {
		return nil, core.NewValidationError(
			fieldName,
			input,
			fmt.Errorf("failed to apply %q input defaults: %w", stepTypeName, err),
		)
	}
	if err := schema.Validate(working); err != nil {
		if runtimeInput, ok := customStepRuntimeValidationInput(schema.Schema(), working); ok {
			if runtimeErr := schema.Validate(runtimeInput); runtimeErr == nil {
				return working, nil
			}
		}
		return nil, core.NewValidationError(
			fieldName,
			input,
			fmt.Errorf("invalid %q input: %w", stepTypeName, err),
		)
	}
	return working, nil
}

func customStepRuntimeValidationInput(root *jsonschema.Schema, input map[string]any) (map[string]any, bool) {
	value, ok := customStepRuntimeValidationValue(root, root, input)
	if !ok {
		return nil, false
	}
	typed, ok := value.(map[string]any)
	return typed, ok
}

func customStepRuntimeValidationValue(root, schema *jsonschema.Schema, value any) (any, bool) {
	schema = customStepRuntimeSchema(root, schema)
	if schema == nil {
		return nil, false
	}

	switch typed := value.(type) {
	case string:
		return customStepRuntimePlaceholder(schema, typed)
	case map[string]any:
		return customStepRuntimeValidationObject(root, schema, typed)
	case []any:
		return customStepRuntimeValidationArray(root, schema, typed)
	default:
		return nil, false
	}
}

func customStepRuntimeValidationObject(root, schema *jsonschema.Schema, value map[string]any) (map[string]any, bool) {
	var output map[string]any
	for key, item := range value {
		propertySchema := customStepObjectPropertySchema(schema, key)
		next, ok := customStepRuntimeValidationValue(root, propertySchema, item)
		if !ok {
			continue
		}
		if output == nil {
			output = make(map[string]any, len(value))
			maps.Copy(output, value)
		}
		output[key] = next
	}
	return output, output != nil
}

func customStepRuntimeValidationArray(root, schema *jsonschema.Schema, value []any) ([]any, bool) {
	var output []any
	for idx, item := range value {
		itemSchema := customStepArrayItemSchema(schema, idx)
		next, ok := customStepRuntimeValidationValue(root, itemSchema, item)
		if !ok {
			continue
		}
		if output == nil {
			output = append([]any(nil), value...)
		}
		output[idx] = next
	}
	return output, output != nil
}

func customStepObjectPropertySchema(schema *jsonschema.Schema, key string) *jsonschema.Schema {
	if schema == nil {
		return nil
	}
	if propertySchema, ok := schema.Properties[key]; ok {
		return propertySchema
	}
	return schema.AdditionalProperties
}

func customStepArrayItemSchema(schema *jsonschema.Schema, idx int) *jsonschema.Schema {
	if schema == nil {
		return nil
	}
	switch {
	case idx < len(schema.PrefixItems):
		return schema.PrefixItems[idx]
	case idx < len(schema.ItemsArray):
		return schema.ItemsArray[idx]
	case schema.Items != nil:
		return schema.Items
	default:
		return schema.AdditionalItems
	}
}

func customStepRuntimePlaceholder(schema *jsonschema.Schema, value string) (any, bool) {
	if !customStepRuntimeExpressionRegexp.MatchString(value) {
		return nil, false
	}

	schemaType, ok := schemaScalarType(schema)
	if !ok && schema.Const != nil {
		schemaType, ok = inferScalarType(*schema.Const)
	}
	if !ok {
		return nil, false
	}

	wholeExpression := customStepWholeRuntimeExpressionRegexp.MatchString(value)
	if schemaType != core.ParamDefTypeString || len(schema.Enum) > 0 || schema.Const != nil {
		if !wholeExpression {
			return nil, false
		}
	}

	return customStepPlaceholderForSchema(schema, schemaType)
}

func customStepPlaceholderForSchema(schema *jsonschema.Schema, schemaType string) (any, bool) {
	if schema.Const != nil {
		return cloneAny(*schema.Const), true
	}
	if len(schema.Enum) > 0 {
		return cloneAny(schema.Enum[0]), true
	}

	switch schemaType {
	case core.ParamDefTypeString:
		return customStepStringPlaceholder(schema), true
	case core.ParamDefTypeInteger:
		return customStepIntegerPlaceholder(schema), true
	case core.ParamDefTypeNumber:
		return customStepNumberPlaceholder(schema), true
	case core.ParamDefTypeBoolean:
		return false, true
	default:
		return nil, false
	}
}

func customStepStringPlaceholder(schema *jsonschema.Schema) string {
	length := 1
	if schema.MaxLength != nil && *schema.MaxLength == 0 {
		length = 0
	}
	if schema.MinLength != nil && *schema.MinLength > length {
		length = *schema.MinLength
	}
	if schema.MaxLength != nil && length > *schema.MaxLength {
		length = *schema.MaxLength
	}
	return strings.Repeat("x", length)
}

func customStepIntegerPlaceholder(schema *jsonschema.Schema) int {
	value := 0
	if schema.Minimum != nil && float64(value) < *schema.Minimum {
		value = ceilInt(*schema.Minimum)
	}
	if schema.ExclusiveMinimum != nil && float64(value) <= *schema.ExclusiveMinimum {
		value = floorInt(*schema.ExclusiveMinimum) + 1
	}
	if schema.Maximum != nil && float64(value) > *schema.Maximum {
		value = floorInt(*schema.Maximum)
	}
	if schema.ExclusiveMaximum != nil && float64(value) >= *schema.ExclusiveMaximum {
		value = ceilInt(*schema.ExclusiveMaximum) - 1
	}
	return value
}

func customStepNumberPlaceholder(schema *jsonschema.Schema) float64 {
	value := 0.0
	if schema.Minimum != nil && value < *schema.Minimum {
		value = *schema.Minimum
	}
	if schema.ExclusiveMinimum != nil && value <= *schema.ExclusiveMinimum {
		value = *schema.ExclusiveMinimum + 1
	}
	if schema.Maximum != nil && value > *schema.Maximum {
		value = *schema.Maximum
	}
	if schema.ExclusiveMaximum != nil && value >= *schema.ExclusiveMaximum {
		value = *schema.ExclusiveMaximum - 1
	}
	return value
}

func customStepRuntimeSchema(root, schema *jsonschema.Schema) *jsonschema.Schema {
	if schema == nil || schema.Ref == "" {
		return schema
	}
	if name, ok := strings.CutPrefix(schema.Ref, "#/$defs/"); ok && root != nil {
		return root.Defs[unescapeJSONPointerSegment(name)]
	}
	if name, ok := strings.CutPrefix(schema.Ref, "#/definitions/"); ok && root != nil {
		return root.Definitions[unescapeJSONPointerSegment(name)]
	}
	return schema
}

func unescapeJSONPointerSegment(segment string) string {
	segment = strings.ReplaceAll(segment, "~1", "/")
	return strings.ReplaceAll(segment, "~0", "~")
}

func ceilInt(value float64) int {
	result := int(value)
	if float64(result) < value {
		result++
	}
	return result
}

func floorInt(value float64) int {
	result := int(value)
	if float64(result) > value {
		result--
	}
	return result
}

func renderCustomStepTemplate(stepTypeName string, template map[string]any, input map[string]any) (map[string]any, error) {
	rendered, err := renderCustomStepTemplateValue(stepTypeName, template, map[string]any{"input": input})
	if err != nil {
		return nil, err
	}
	result, ok := rendered.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("custom step template for %q must render to an object", stepTypeName)
	}
	return result, nil
}

func renderCustomStepTemplateValue(stepTypeName string, value any, data map[string]any) (any, error) {
	switch typed := value.(type) {
	case nil:
		return nil, nil
	case string:
		return renderCustomStepTemplateString(stepTypeName, typed, data)
	case []any:
		rendered := make([]any, 0, len(typed))
		for _, item := range typed {
			v, err := renderCustomStepTemplateValue(stepTypeName, item, data)
			if err != nil {
				return nil, err
			}
			rendered = append(rendered, v)
		}
		return rendered, nil
	case map[string]any:
		if refPath, ok := typed["$input"].(string); ok && len(typed) == 1 {
			resolved, err := resolveCustomStepInputRef(data["input"], refPath)
			if err != nil {
				return nil, fmt.Errorf("failed to resolve %q template input %q: %w", stepTypeName, refPath, err)
			}
			return resolved, nil
		}
		rendered := make(map[string]any, len(typed))
		for key, item := range typed {
			v, err := renderCustomStepTemplateValue(stepTypeName, item, data)
			if err != nil {
				return nil, err
			}
			rendered[key] = v
		}
		return rendered, nil
	default:
		return typed, nil
	}
}

func renderCustomStepTemplateString(stepTypeName string, text string, data map[string]any) (string, error) {
	funcs := templatefuncs.FuncMap()
	funcs["json"] = func(v any) (string, error) {
		raw, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		return string(raw), nil
	}

	tmpl, err := gotemplate.New(stepTypeName).
		Option("missingkey=error").
		Funcs(funcs).
		Parse(text)
	if err != nil {
		return "", fmt.Errorf("failed to parse template string: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute template string: %w", err)
	}
	return buf.String(), nil
}

func resolveCustomStepInputRef(input any, path string) (any, error) {
	current := input
	for segment := range strings.SplitSeq(strings.TrimSpace(path), ".") {
		if segment == "" {
			return nil, fmt.Errorf("path contains an empty segment")
		}
		switch typed := current.(type) {
		case map[string]any:
			next, ok := typed[segment]
			if !ok {
				return nil, fmt.Errorf("field %q does not exist", segment)
			}
			current = next
		case []any:
			index, err := strconv.Atoi(segment)
			if err != nil {
				return nil, fmt.Errorf("segment %q is not a valid array index", segment)
			}
			if index < 0 || index >= len(typed) {
				return nil, fmt.Errorf("array index %d is out of range", index)
			}
			current = typed[index]
		default:
			return nil, fmt.Errorf("segment %q cannot be resolved from %T", segment, current)
		}
	}
	return current, nil
}

func cloneMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = cloneAny(value)
	}
	return dst
}

func cloneAny(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneMap(typed)
	case []any:
		dst := make([]any, len(typed))
		for i, item := range typed {
			dst[i] = cloneAny(item)
		}
		return dst
	default:
		return typed
	}
}

func buildCustomStepFromSpec(
	ctx StepBuildContext,
	callSite *step,
	raw map[string]any,
	defs *defaults,
	customType *customStepType,
	forcedName bool,
) (*core.Step, error) {
	if err := validateCustomStepCallSiteFields(callSite, raw); err != nil {
		return nil, fmt.Errorf("step type %q: %w", customType.Name, err)
	}

	input := map[string]any{}
	if config := callSite.executorConfig(); config != nil {
		input = cloneMap(config)
	}
	validatedInput, err := validateCustomStepInput(customType.Name, customType.InputSchema, callSite.executorConfigFieldName(), input)
	if err != nil {
		return nil, err
	}

	rendered, err := renderCustomStepTemplate(customType.Name, customType.Template, validatedInput)
	if err != nil {
		return nil, core.NewValidationError(
			fmt.Sprintf("step_types.%s.template", customType.Name),
			customType.Template,
			err,
		)
	}
	mergedRaw, err := mergeCustomStepRaw(rendered, callSite, raw, customType, forcedName)
	if err != nil {
		return nil, fmt.Errorf("step type %q: %w", customType.Name, err)
	}
	normalizedRaw, err := normalizeStepExecutionRaw(mergedRaw, nil)
	if err != nil {
		return nil, fmt.Errorf("step type %q: failed to normalize expanded template: %w", customType.Name, err)
	}

	expandedSpec, err := decodeStep(normalizedRaw)
	if err != nil {
		return nil, fmt.Errorf("step type %q: failed to decode expanded template: %w", customType.Name, err)
	}
	applyDefaults(expandedSpec, defs, normalizedRaw)
	builtStep, err := buildConcreteStep(ctx, expandedSpec)
	if err != nil {
		return nil, fmt.Errorf("step type %q (resolves to %q): %w", customType.Name, customType.Type, err)
	}
	if builtStep.ExecutorConfig.Metadata == nil {
		builtStep.ExecutorConfig.Metadata = make(map[string]any, 1)
	}
	builtStep.ExecutorConfig.Metadata["custom_type"] = customType.Name
	if customType.Description != "" && builtStep.Description == "" {
		builtStep.Description = customType.Description
	}
	return builtStep, nil
}

func mergeCustomStepRaw(
	rendered map[string]any,
	callSite *step,
	raw map[string]any,
	customType *customStepType,
	forcedName bool,
) (map[string]any, error) {
	merged := cloneMap(rendered)
	if expandedType := expandedCustomStepExecutorType(customType.Type, rendered); expandedType != "" {
		merged["type"] = expandedType
	}

	callSiteRaw, err := customStepCallSiteRaw(callSite, raw, forcedName)
	if err != nil {
		return nil, err
	}
	for key, value := range callSiteRaw {
		switch key {
		case "config", "with", "type":
			continue
		case "env":
			combined, err := mergeCustomStepEnvRaw(merged[key], value)
			if err != nil {
				return nil, core.NewValidationError("env", value, err)
			}
			merged[key] = combined
		case "preconditions":
			if current := merged[key]; current != nil {
				merged[key] = combinePreconditions(current, cloneAny(value))
			} else {
				merged[key] = cloneAny(value)
			}
		default:
			merged[key] = cloneAny(value)
		}
	}

	return merged, nil
}

func customStepCallSiteRaw(callSite *step, raw map[string]any, forcedName bool) (map[string]any, error) {
	if raw != nil {
		cloned := cloneMap(raw)
		if forcedName && callSite != nil {
			cloned["name"] = callSite.Name
		}
		return cloned, nil
	}
	if callSite == nil {
		return nil, nil
	}

	yamlBytes, err := yaml.Marshal(callSite)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal custom step call site: %w", err)
	}

	var decoded map[string]any
	if err := yaml.Unmarshal(yamlBytes, &decoded); err != nil {
		return nil, fmt.Errorf("failed to decode custom step call site: %w", err)
	}
	if forcedName {
		decoded["name"] = callSite.Name
	}
	return decoded, nil
}

func mergeCustomStepEnvRaw(base, override any) (any, error) {
	switch {
	case base == nil:
		return cloneAny(override), nil
	case override == nil:
		return cloneAny(base), nil
	}

	baseEnv, err := decodeViaYAML[types.EnvValue](base)
	if err != nil {
		return nil, fmt.Errorf("invalid template env: %w", err)
	}
	overrideEnv, err := decodeViaYAML[types.EnvValue](override)
	if err != nil {
		return nil, fmt.Errorf("invalid call-site env: %w", err)
	}

	combined := overrideEnv.Prepend(baseEnv)
	return envValueToRaw(combined), nil
}

func envValueToRaw(value types.EnvValue) any {
	entries := value.Entries()
	if len(entries) == 0 {
		return nil
	}

	raw := make([]any, 0, len(entries))
	for _, entry := range entries {
		raw = append(raw, map[string]any{entry.Key: entry.Value})
	}
	return raw
}

func validateCustomStepCallSiteFields(callSite *step, raw map[string]any) error {
	if raw != nil {
		if err := validateStepConfigAliasRaw(raw); err != nil {
			return err
		}
		for key := range raw {
			if key == "config" || key == "with" || key == "type" {
				continue
			}
			if _, ok := customStepForbiddenCallSiteFields[key]; ok {
				return core.NewValidationError(key, raw[key], fmt.Errorf("field %q is not allowed when using a custom step type", key))
			}
		}
		return nil
	}

	if callSite == nil {
		return nil
	}
	if err := validateStepConfigAliasStruct(callSite); err != nil {
		return err
	}
	if callSite.WorkingDir != "" {
		return core.NewValidationError("working_dir", callSite.WorkingDir, fmt.Errorf("field %q is not allowed when using a custom step type", "working_dir"))
	}
	if callSite.Command != nil {
		return core.NewValidationError("command", callSite.Command, fmt.Errorf("field %q is not allowed when using a custom step type", "command"))
	}
	if callSite.Exec != nil {
		return core.NewValidationError("exec", callSite.Exec, fmt.Errorf("field %q is not allowed when using a custom step type", "exec"))
	}
	if !callSite.Shell.IsZero() {
		return core.NewValidationError("shell", callSite.Shell.Value(), fmt.Errorf("field %q is not allowed when using a custom step type", "shell"))
	}
	if len(callSite.ShellArgs) > 0 {
		return core.NewValidationError("shell_args", callSite.ShellArgs, fmt.Errorf("field %q is not allowed when using a custom step type", "shell_args"))
	}
	if len(callSite.ShellPackages) > 0 {
		return core.NewValidationError("shell_packages", callSite.ShellPackages, fmt.Errorf("field %q is not allowed when using a custom step type", "shell_packages"))
	}
	if callSite.Script != "" {
		return core.NewValidationError("script", callSite.Script, fmt.Errorf("field %q is not allowed when using a custom step type", "script"))
	}
	if callSite.Call != "" {
		return core.NewValidationError("call", callSite.Call, fmt.Errorf("field %q is not allowed when using a custom step type", "call"))
	}
	if callSite.Params != nil {
		return core.NewValidationError("params", callSite.Params, fmt.Errorf("field %q is not allowed when using a custom step type", "params"))
	}
	if callSite.Parallel != nil {
		return core.NewValidationError("parallel", callSite.Parallel, fmt.Errorf("field %q is not allowed when using a custom step type", "parallel"))
	}
	if callSite.Container != nil {
		return core.NewValidationError("container", callSite.Container, fmt.Errorf("field %q is not allowed when using a custom step type", "container"))
	}
	if callSite.LLM != nil {
		return core.NewValidationError("llm", callSite.LLM, fmt.Errorf("field %q is not allowed when using a custom step type", "llm"))
	}
	if len(callSite.Messages) > 0 {
		return core.NewValidationError("messages", callSite.Messages, fmt.Errorf("field %q is not allowed when using a custom step type", "messages"))
	}
	if len(callSite.Routes) > 0 {
		return core.NewValidationError("routes", callSite.Routes, fmt.Errorf("field %q is not allowed when using a custom step type", "routes"))
	}
	if strings.TrimSpace(callSite.Value) != "" {
		return core.NewValidationError("value", callSite.Value, fmt.Errorf("field %q is not allowed when using a custom step type", "value"))
	}
	return nil
}

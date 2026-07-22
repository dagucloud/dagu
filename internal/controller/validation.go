// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package controller

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
	"unicode"
	"unicode/utf8"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/core/spec"
	"gopkg.in/yaml.v3"
)

const (
	maxNameRunes        = 100
	maxNameBytes        = 256
	maxDescriptionBytes = 4 << 10
	maxSystemBytes      = 16 << 10
	maxPromptBytes      = 16 << 10
	maxStates           = 64
	maxDAGs             = 64
	maxTransitions      = 256
	minMaxTurns         = 2
	maxMaxTurns         = 1000
)

var (
	controllerIDPattern = regexp.MustCompile(`^ctrl_[a-z2-7]{16}$`)
	stateNamePattern    = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)
)

type createDefinition struct {
	Type        string                    `yaml:"type"`
	Version     int                       `yaml:"version"`
	Name        string                    `yaml:"name"`
	Description string                    `yaml:"description"`
	MaxTurns    int                       `yaml:"maxTurns,omitempty"`
	Labels      []string                  `yaml:"labels,omitempty"`
	LLM         ControllerRouterLLMConfig `yaml:"llm"`
	DAGs        []string                  `yaml:"dags"`
	States      map[string]State          `yaml:"states"`
}

// DAGReference contains the definition facts required for Controller validation.
type DAGReference struct {
	FileName            string
	Name                string
	Workspace           string
	Description         string
	HasPositionalParams bool
}

// DAGResolver resolves a Controller DAG fileName using the current DAG definitions.
type DAGResolver interface {
	ResolveDAG(ctx context.Context, fileName string) (DAGReference, error)
}

// DAGResolverFunc adapts a function to DAGResolver.
type DAGResolverFunc func(ctx context.Context, fileName string) (DAGReference, error)

// ResolveDAG implements DAGResolver.
func (f DAGResolverFunc) ResolveDAG(ctx context.Context, fileName string) (DAGReference, error) {
	return f(ctx, fileName)
}

type dagStoreResolver struct {
	store exec.DAGStore
}

// NewDAGStoreResolver adapts the existing DAG store to Controller validation.
func NewDAGStoreResolver(store exec.DAGStore) DAGResolver {
	if store == nil {
		return nil
	}
	return &dagStoreResolver{store: store}
}

func (r *dagStoreResolver) ResolveDAG(ctx context.Context, fileName string) (DAGReference, error) {
	dag, err := r.store.GetDetails(ctx, fileName, spec.WithoutEval())
	if err != nil {
		return DAGReference{}, err
	}
	if dag == nil {
		return DAGReference{}, fmt.Errorf("DAG %q resolved to nil", fileName)
	}
	workspaceName, state := exec.WorkspaceLabelFromLabels(dag.Labels)
	if state == exec.WorkspaceLabelInvalid {
		return DAGReference{}, fmt.Errorf("DAG %q has invalid workspace labels", fileName)
	}
	hasPositional := false
	for _, param := range dag.ParamDefs {
		if param.Name == "" {
			hasPositional = true
			break
		}
	}
	return DAGReference{
		FileName:            dag.FileName(),
		Name:                dag.Name,
		Workspace:           workspaceName,
		Description:         dag.Description,
		HasPositionalParams: hasPositional,
	}, nil
}

// Validator performs static and current-DAG validation for Controller definitions.
type Validator struct {
	resolver DAGResolver
}

// NewValidator creates a strict definition validator. A nil resolver performs static validation only.
func NewValidator(resolver DAGResolver) *Validator {
	return &Validator{resolver: resolver}
}

// ParseCreateDefinition strictly decodes an ID-less create document.
func ParseCreateDefinition(data []byte) (*Definition, error) {
	var draft createDefinition
	if err := decodeStrictYAML(data, &draft); err != nil {
		return nil, err
	}
	definition := &Definition{
		Type:        draft.Type,
		Version:     draft.Version,
		Name:        draft.Name,
		Description: draft.Description,
		MaxTurns:    draft.MaxTurns,
		Labels:      draft.Labels,
		LLM:         draft.LLM,
		DAGs:        draft.DAGs,
		States:      draft.States,
	}
	if yamlPathPresent(data, "maxTurns") && definition.MaxTurns == 0 {
		return nil, &ValidationError{Issues: []ValidationIssue{issue("invalid_range", "maxTurns", fmt.Sprintf("must be between %d and %d", minMaxTurns, maxMaxTurns))}}
	}
	if yamlPathPresent(data, "llm", "system") && definition.LLM.System == nil {
		return nil, &ValidationError{Issues: []ValidationIssue{issue("invalid_system_template", "llm.system", "must be a string when present")}}
	}
	if err := validateDefinitionStatic(definition, false); err != nil {
		return nil, err
	}
	return definition, nil
}

// ParseDefinition strictly decodes and validates a persisted Controller document.
func ParseDefinition(data []byte) (*Definition, error) {
	var definition Definition
	if err := decodeStrictYAML(data, &definition); err != nil {
		return nil, err
	}
	if yamlPathPresent(data, "maxTurns") && definition.MaxTurns == 0 {
		return nil, &ValidationError{Issues: []ValidationIssue{issue("invalid_range", "maxTurns", fmt.Sprintf("must be between %d and %d", minMaxTurns, maxMaxTurns))}}
	}
	if yamlPathPresent(data, "llm", "system") && definition.LLM.System == nil {
		return nil, &ValidationError{Issues: []ValidationIssue{issue("invalid_system_template", "llm.system", "must be a string when present")}}
	}
	if err := validateDefinitionStatic(&definition, true); err != nil {
		return nil, err
	}
	return &definition, nil
}

// MarshalDefinition emits the canonical persisted YAML representation.
func MarshalDefinition(definition *Definition) ([]byte, error) {
	if err := validateDefinitionStatic(definition, true); err != nil {
		return nil, err
	}
	data, err := yaml.Marshal(definition)
	if err != nil {
		return nil, fmt.Errorf("marshal controller definition: %w", err)
	}
	return data, nil
}

// Validate verifies a persisted definition and all current DAG references.
func (v *Validator) Validate(ctx context.Context, definition *Definition) error {
	if err := validateDefinitionStatic(definition, true); err != nil {
		return err
	}
	if v == nil || v.resolver == nil {
		return nil
	}

	workspaceName := definition.Workspace()
	var issues []ValidationIssue
	for index, fileName := range definition.DAGs {
		ref, err := v.resolver.ResolveDAG(ctx, fileName)
		path := fmt.Sprintf("dags[%d]", index)
		if err != nil {
			issues = append(issues, issue("dag_not_found", path, fmt.Sprintf("DAG %q cannot be loaded: %v", fileName, err)))
			continue
		}
		if ref.FileName != fileName || ref.Name != fileName {
			issues = append(issues, issue("dag_identity_mismatch", path, fmt.Sprintf("DAG fileName and name must both equal %q", fileName)))
		}
		if ref.Workspace != workspaceName {
			issues = append(issues, issue("dag_workspace_mismatch", path, fmt.Sprintf("DAG %q is in a different workspace", fileName)))
		}
		if ref.HasPositionalParams {
			issues = append(issues, issue("dag_positional_params", path, fmt.Sprintf("DAG %q uses unsupported positional parameters", fileName)))
		}
	}
	return validationIssuesError(issues)
}

// ValidatePrompt enforces the Start and Prompt input contract without normalizing the input.
func ValidatePrompt(prompt string) error {
	if !utf8.ValidString(prompt) || strings.TrimSpace(prompt) == "" {
		return fmt.Errorf("%w: prompt must contain non-whitespace UTF-8 text", ErrInvalidPrompt)
	}
	if len(prompt) > maxPromptBytes {
		return fmt.Errorf("%w: prompt exceeds %d bytes", ErrInvalidPrompt, maxPromptBytes)
	}
	return nil
}

func decodeStrictYAML(data []byte, target any) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(target); err != nil {
		if errors.Is(err, io.EOF) {
			return &ValidationError{Issues: []ValidationIssue{issue("invalid_yaml", "", "definition is empty")}}
		}
		return &ValidationError{Issues: []ValidationIssue{issue("invalid_yaml", "", err.Error())}}
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return &ValidationError{Issues: []ValidationIssue{issue("invalid_yaml", "", err.Error())}}
		}
		return &ValidationError{Issues: []ValidationIssue{issue("invalid_yaml", "", "multiple YAML documents are not allowed")}}
	}
	return nil
}

func yamlPathPresent(data []byte, path ...string) bool {
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil || len(document.Content) == 0 {
		return false
	}
	node := document.Content[0]
	for _, segment := range path {
		if node.Kind != yaml.MappingNode {
			return false
		}
		var next *yaml.Node
		for index := 0; index+1 < len(node.Content); index += 2 {
			if node.Content[index].Value == segment {
				next = node.Content[index+1]
				break
			}
		}
		if next == nil {
			return false
		}
		node = next
	}
	return true
}

func validateDefinitionStatic(definition *Definition, requireID bool) error {
	if definition == nil {
		return &ValidationError{Issues: []ValidationIssue{issue("required", "", "definition is required")}}
	}
	var issues []ValidationIssue
	if definition.Type != DefinitionType {
		issues = append(issues, issue("invalid_value", "type", fmt.Sprintf("must be %q", DefinitionType)))
	}
	if definition.Version != DefinitionVersion {
		issues = append(issues, issue("invalid_value", "version", fmt.Sprintf("must be %d", DefinitionVersion)))
	}
	if requireID {
		if !controllerIDPattern.MatchString(definition.ID) {
			issues = append(issues, issue("invalid_id", "id", "must match ^ctrl_[a-z2-7]{16}$"))
		}
	} else if definition.ID != "" {
		issues = append(issues, issue("generated_field", "id", "must not be supplied when creating a Controller"))
	}
	issues = append(issues, validateName(definition.Name)...)
	if strings.TrimSpace(definition.Description) == "" {
		issues = append(issues, issue("required", "description", "must contain non-whitespace text"))
	}
	if len(definition.Description) > maxDescriptionBytes {
		issues = append(issues, issue("size_limit", "description", fmt.Sprintf("must not exceed %d bytes", maxDescriptionBytes)))
	}
	if definition.MaxTurns != 0 && (definition.MaxTurns < minMaxTurns || definition.MaxTurns > maxMaxTurns) {
		issues = append(issues, issue("invalid_range", "maxTurns", fmt.Sprintf("must be between %d and %d", minMaxTurns, maxMaxTurns)))
	}
	issues = append(issues, validateLabels(definition.Labels)...)
	issues = append(issues, validateLLM(definition.LLM)...)
	issues = append(issues, validateGraph(definition)...)
	return validationIssuesError(issues)
}

func validateName(name string) []ValidationIssue {
	var issues []ValidationIssue
	if !utf8.ValidString(name) || name == "" {
		return []ValidationIssue{issue("required", "name", "must be valid non-empty UTF-8")}
	}
	if utf8.RuneCountInString(name) > maxNameRunes || len(name) > maxNameBytes {
		issues = append(issues, issue("size_limit", "name", fmt.Sprintf("must not exceed %d code points or %d bytes", maxNameRunes, maxNameBytes)))
	}
	if strings.ContainsAny(name, "\r\n") {
		issues = append(issues, issue("single_line", "name", "must be a single line"))
	}
	first, _ := utf8.DecodeRuneInString(name)
	last, _ := utf8.DecodeLastRuneInString(name)
	if unicode.IsSpace(first) || unicode.IsSpace(last) {
		issues = append(issues, issue("surrounding_whitespace", "name", "must not have leading or trailing whitespace"))
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			issues = append(issues, issue("control_character", "name", "must not contain control characters"))
			break
		}
	}
	return issues
}

func validateLabels(raw []string) []ValidationIssue {
	labels := core.NewLabels(raw)
	var issues []ValidationIssue
	if len(labels) != len(raw) {
		issues = append(issues, issue("invalid_label", "labels", "labels must not be empty"))
	}
	if err := core.ValidateLabels(labels); err != nil {
		issues = append(issues, issue("invalid_label", "labels", err.Error()))
	}
	workspaceCount := 0
	for _, label := range labels {
		if label.Key == exec.WorkspaceLabelKey {
			workspaceCount++
		}
	}
	if workspaceCount > 1 {
		issues = append(issues, issue("workspace_label", "labels", "at most one workspace label is allowed"))
	}
	if _, state := exec.WorkspaceLabelFromLabels(labels); state == exec.WorkspaceLabelInvalid {
		issues = append(issues, issue("workspace_label", "labels", "workspace label is invalid"))
	}
	return issues
}

func validateLLM(config ControllerRouterLLMConfig) []ValidationIssue {
	var issues []ValidationIssue
	switch config.Provider {
	case "openai", "anthropic", "gemini":
	case "":
		issues = append(issues, issue("required", "llm.provider", "is required"))
	default:
		issues = append(issues, issue("unsupported_provider", "llm.provider", "must be one of openai, anthropic, or gemini"))
	}
	if strings.TrimSpace(config.Model) == "" {
		issues = append(issues, issue("required", "llm.model", "must contain non-whitespace text"))
	}
	source := config.EffectiveSystem()
	if len(source) > maxSystemBytes {
		issues = append(issues, issue("size_limit", "llm.system", fmt.Sprintf("must not exceed %d bytes", maxSystemBytes)))
		return issues
	}
	if source != RouterInstructionPattern {
		prefix := RouterInstructionPattern + "\n\n"
		if !strings.HasPrefix(source, prefix) {
			issues = append(issues, issue("invalid_system_template", "llm.system", "must begin with the reserved Router instruction followed by exactly two newlines"))
			return issues
		}
		suffix := strings.TrimPrefix(source, prefix)
		if strings.TrimSpace(suffix) == "" {
			issues = append(issues, issue("invalid_system_template", "llm.system", "literal policy suffix must contain non-whitespace text"))
		}
		if strings.Contains(suffix, "${{") {
			issues = append(issues, issue("invalid_system_template", "llm.system", "literal policy suffix must not contain ${{"))
		}
	}
	tmpl, err := template.New("controller-router-system").Delims("${{", "}}").Option("missingkey=error").Parse(source)
	if err == nil {
		var rendered bytes.Buffer
		err = tmpl.Execute(&rendered, struct{ RouterInstruction string }{RouterInstruction: "router"})
	}
	if err != nil {
		issues = append(issues, issue("invalid_system_template", "llm.system", err.Error()))
	}
	return issues
}

func validateGraph(definition *Definition) []ValidationIssue {
	var issues []ValidationIssue
	if definition.DAGs == nil {
		issues = append(issues, issue("required", "dags", "must be present; use an empty list when no DAGs are allowed"))
	}
	if len(definition.DAGs) > maxDAGs {
		issues = append(issues, issue("size_limit", "dags", fmt.Sprintf("must not contain more than %d entries", maxDAGs)))
	}
	allowedDAGs := make(map[string]struct{}, len(definition.DAGs))
	for index, fileName := range definition.DAGs {
		path := fmt.Sprintf("dags[%d]", index)
		if !validDAGFileName(fileName) {
			issues = append(issues, issue("invalid_dag_name", path, "must be a non-empty extensionless fileName without path separators"))
		}
		if _, duplicate := allowedDAGs[fileName]; duplicate {
			issues = append(issues, issue("duplicate", path, fmt.Sprintf("DAG %q is listed more than once", fileName)))
		}
		allowedDAGs[fileName] = struct{}{}
	}
	if len(definition.States) == 0 {
		issues = append(issues, issue("required", "states", "must contain at least the default State"))
		return issues
	}
	if len(definition.States) > maxStates {
		issues = append(issues, issue("size_limit", "states", fmt.Sprintf("must not contain more than %d States", maxStates)))
	}
	if _, ok := definition.States[DefaultStateName]; !ok {
		issues = append(issues, issue("required", "states.default", "default State is required"))
	}
	totalTransitions := 0
	for name, state := range definition.States {
		basePath := "states." + name
		if !stateNamePattern.MatchString(name) {
			issues = append(issues, issue("invalid_state_name", basePath, "State name must match ^[A-Za-z][A-Za-z0-9_-]{0,63}$"))
		}
		if strings.TrimSpace(state.Description) == "" {
			issues = append(issues, issue("required", basePath+".description", "must contain non-whitespace text"))
		}
		if len(state.Description) > maxDescriptionBytes {
			issues = append(issues, issue("size_limit", basePath+".description", fmt.Sprintf("must not exceed %d bytes", maxDescriptionBytes)))
		}
		stateDAGs := make(map[string]struct{}, len(state.DAGs))
		for index, fileName := range state.DAGs {
			path := fmt.Sprintf("%s.dags[%d]", basePath, index)
			if _, ok := allowedDAGs[fileName]; !ok {
				issues = append(issues, issue("dag_not_allowed", path, fmt.Sprintf("DAG %q is not in the top-level allowlist", fileName)))
			}
			if _, duplicate := stateDAGs[fileName]; duplicate {
				issues = append(issues, issue("duplicate", path, fmt.Sprintf("DAG %q is listed more than once in this State", fileName)))
			}
			stateDAGs[fileName] = struct{}{}
		}
		totalTransitions += len(state.Transitions)
		destinations := make(map[string]struct{}, len(state.Transitions))
		for index, transition := range state.Transitions {
			path := fmt.Sprintf("%s.transitions[%d]", basePath, index)
			if !stateNamePattern.MatchString(transition.To) {
				issues = append(issues, issue("invalid_state_name", path+".to", "must be a valid State name"))
			} else if _, ok := definition.States[transition.To]; !ok {
				issues = append(issues, issue("unknown_state", path+".to", fmt.Sprintf("State %q does not exist", transition.To)))
			}
			if _, duplicate := destinations[transition.To]; duplicate {
				issues = append(issues, issue("duplicate", path+".to", fmt.Sprintf("State %q is already a transition destination", transition.To)))
			}
			destinations[transition.To] = struct{}{}
			if strings.TrimSpace(transition.When) == "" {
				issues = append(issues, issue("required", path+".when", "must contain non-whitespace text"))
			}
			if len(transition.When) > maxDescriptionBytes {
				issues = append(issues, issue("size_limit", path+".when", fmt.Sprintf("must not exceed %d bytes", maxDescriptionBytes)))
			}
		}
		switch state.Terminal {
		case "":
			if len(state.DAGs) == 0 && len(state.Transitions) == 0 {
				issues = append(issues, issue("structural_dead_end", basePath, "non-terminal State must allow a DAG or an outgoing transition"))
			}
		case "succeeded", "failed":
			if len(state.DAGs) != 0 || len(state.Transitions) != 0 {
				issues = append(issues, issue("terminal_state", basePath, "terminal State must not contain DAGs or transitions"))
			}
		default:
			issues = append(issues, issue("invalid_terminal", basePath+".terminal", "must be succeeded or failed"))
		}
	}
	if totalTransitions > maxTransitions {
		issues = append(issues, issue("size_limit", "states", fmt.Sprintf("must not contain more than %d transitions in total", maxTransitions)))
	}
	return issues
}

func validDAGFileName(name string) bool {
	return name != "" && name != "." && name != ".." && filepath.Base(name) == name && filepath.Ext(name) == "" && !strings.ContainsAny(name, `/\\`)
}

func issue(code, path, message string) ValidationIssue {
	return ValidationIssue{Code: code, Path: path, Message: message}
}

func validationIssuesError(issues []ValidationIssue) error {
	if len(issues) == 0 {
		return nil
	}
	return &ValidationError{Issues: issues}
}

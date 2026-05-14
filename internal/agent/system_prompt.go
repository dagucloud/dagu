// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/workspace"
)

//go:embed system_prompt.txt
var systemPromptRaw string

// systemPromptTemplate is parsed once at package initialization.
var systemPromptTemplate = template.Must(
	template.New("system_prompt").Parse(systemPromptRaw),
)

// CurrentDAG contains context about the DAG being viewed.
type CurrentDAG struct {
	Name     string
	FilePath string
	RunID    string
	Status   string
}

// UserCapabilities contains role and capability context for the current user.
type UserCapabilities struct {
	Role           string
	CanExecuteDAGs bool
	CanWriteDAGs   bool
	CanViewAudit   bool
	IsAdmin        bool
}

// soulPlaceholder is an internal marker that replaces soul content during
// template execution to prevent user-controlled content from being interpreted
// as Go template directives.
const soulPlaceholder = "\x00__SOUL_CONTENT__\x00"

// systemPromptData contains all data for template rendering.
type systemPromptData struct {
	EnvironmentInfo
	CurrentDAG  *CurrentDAG
	Memory      MemoryContent
	User        *UserCapabilities
	SoulContent string
	Actions     string
}

// SystemPromptParams holds all parameters for system prompt generation.
type SystemPromptParams struct {
	Env             EnvironmentInfo
	CurrentDAG      *CurrentDAG
	Memory          MemoryContent
	Role            auth.Role
	WorkspaceAccess *auth.WorkspaceAccess
	Soul            *Soul
}

// GenerateSystemPrompt renders the system prompt template with the given parameters.
func GenerateSystemPrompt(p SystemPromptParams) string {
	env := p.Env
	currentDAG := p.CurrentDAG
	memory := p.Memory
	role := p.Role
	soul := p.Soul
	var buf bytes.Buffer
	var rawSoulContent string
	if soul != nil {
		rawSoulContent = soul.Content
	}
	// Use a placeholder during template execution to prevent user-controlled
	// soul content from being interpreted as Go template directives.
	templateSoulContent := soulPlaceholder
	if rawSoulContent == "" {
		templateSoulContent = ""
	}
	data := systemPromptData{
		EnvironmentInfo: env,
		CurrentDAG:      currentDAG,
		Memory:          memory,
		User:            buildUserCapabilities(role),
		SoulContent:     templateSoulContent,
		Actions:         buildActionsPrompt(env, p.WorkspaceAccess),
	}
	if err := systemPromptTemplate.Execute(&buf, data); err != nil {
		return fallbackPrompt(env)
	}
	result := buf.String()
	if rawSoulContent != "" {
		result = strings.Replace(result, soulPlaceholder, rawSoulContent, 1)
	}
	return result
}

type customActionSource struct {
	label             string
	actions           []customActionRef
	legacyDefinitions []legacyDefinitionRef
	err               error
}

type customActionRef struct {
	name string
}

type legacyDefinitionRef struct {
	name       string
	targetType string
}

func buildActionsPrompt(env EnvironmentInfo, access *auth.WorkspaceAccess) string {
	var b strings.Builder
	b.WriteString("Available actions are generated from the built-in action registry and base config files.\n")
	b.WriteString("- Builtin actions: ")
	b.WriteString(formatNames(spec.BuiltinActionNames()))
	b.WriteString(". Use top-level `run:` for plain shell commands and scripts.\n")

	sources := baseConfigCustomActionSources(env, access)
	if len(sources) == 0 {
		b.WriteString("- Base config custom actions: none found in configured base config files.\n")
	} else {
		b.WriteString("- Base config custom actions:\n")
		for _, source := range sources {
			b.WriteString("  - ")
			b.WriteString(source.label)
			b.WriteString(": ")
			if source.err != nil {
				b.WriteString("unable to inspect")
				if msg := strings.TrimSpace(source.err.Error()); msg != "" {
					b.WriteString(": ")
					b.WriteString(msg)
				}
				b.WriteString(".\n")
				continue
			}
			if len(source.actions) == 0 {
				b.WriteString("none")
				b.WriteString(".\n")
			} else {
				b.WriteString(formatCustomActionRefs(source.actions))
				b.WriteString(".\n")
			}
			if len(source.legacyDefinitions) > 0 {
				b.WriteString("  - ")
				b.WriteString(source.label)
				b.WriteString(" legacy `step_types:` definitions: ")
				b.WriteString(formatLegacyDefinitionRefs(source.legacyDefinitions))
				b.WriteString(". Prefer `actions:` for new work.\n")
			}
		}
	}

	b.WriteString("- Current DAG-local custom actions: inspect `actions:` in the DAG before deciding availability.\n")
	b.WriteString("- Legacy DAG-local `step_types:` definitions may still exist for compatibility; prefer `actions:` for new work.\n")
	b.WriteString("- For custom actions, pass only declared `with:` inputs; do not add executor fields hidden by the template.")
	return strings.TrimRight(b.String(), "\n")
}

func baseConfigCustomActionSources(env EnvironmentInfo, access *auth.WorkspaceAccess) []customActionSource {
	var sources []customActionSource
	if strings.TrimSpace(env.BaseConfigFile) != "" {
		if _, err := os.Stat(env.BaseConfigFile); err == nil {
			sources = append(sources, customActionSourceFromFile(
				fmt.Sprintf("global Base Config (`%s`)", env.BaseConfigFile),
				env.BaseConfigFile,
			))
		} else if !os.IsNotExist(err) {
			sources = append(sources, customActionSource{
				label: fmt.Sprintf("global Base Config (`%s`)", env.BaseConfigFile),
				err:   err,
			})
		}
	}

	workspaceDir := workspace.BaseConfigDir(env.DAGsDir)
	if workspaceDir == "" {
		return sources
	}
	info, err := os.Stat(workspaceDir)
	if err != nil {
		if !os.IsNotExist(err) {
			sources = append(sources, customActionSource{
				label: fmt.Sprintf("workspace base config directory (`%s`)", workspaceDir),
				err:   err,
			})
		}
		return sources
	}
	if !info.IsDir() {
		sources = append(sources, customActionSource{
			label: fmt.Sprintf("workspace base config directory (`%s`)", workspaceDir),
			err:   fmt.Errorf("%s is not a directory", workspaceDir),
		})
		return sources
	}
	entries, err := os.ReadDir(workspaceDir)
	if err != nil {
		if !os.IsNotExist(err) {
			sources = append(sources, customActionSource{
				label: fmt.Sprintf("workspace base config directory (`%s`)", workspaceDir),
				err:   err,
			})
		}
		return sources
	}
	allWorkspaces, allowedWorkspaces := allowedWorkspaceActionSources(access)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		workspaceName := entry.Name()
		if !allWorkspaces {
			if _, ok := allowedWorkspaces[workspaceName]; !ok {
				continue
			}
		}
		if err := workspace.ValidateName(workspaceName); err != nil {
			sources = append(sources, customActionSource{
				label: fmt.Sprintf("workspace `%s` base config", workspaceName),
				err:   err,
			})
			continue
		}
		path := filepath.Join(workspaceDir, workspaceName, workspace.BaseConfigFileName)
		if _, err := os.Stat(path); err != nil {
			if !os.IsNotExist(err) {
				sources = append(sources, customActionSource{
					label: fmt.Sprintf("workspace `%s` base config (`%s`)", workspaceName, path),
					err:   err,
				})
			}
			continue
		}
		sources = append(sources, customActionSourceFromFile(
			fmt.Sprintf("workspace `%s` base config", workspaceName),
			path,
		))
	}
	return sources
}

func allowedWorkspaceActionSources(access *auth.WorkspaceAccess) (bool, map[string]struct{}) {
	normalized := auth.NormalizeWorkspaceAccess(access)
	if normalized.All {
		return true, nil
	}
	allowed := make(map[string]struct{}, len(normalized.Grants))
	for _, grant := range normalized.Grants {
		name := strings.TrimSpace(grant.Workspace)
		if name != "" {
			allowed[name] = struct{}{}
		}
	}
	return false, allowed
}

func customActionSourceFromFile(label, path string) customActionSource {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return customActionSource{label: label, err: err}
	}
	actionHints, err := spec.InheritedCustomActionEditorHints(data)
	if err != nil {
		return customActionSource{label: label, err: err}
	}
	hints, err := spec.InheritedLegacyDefinitionEditorHints(data)
	if err != nil {
		return customActionSource{label: label, err: err}
	}
	actions := make([]customActionRef, 0, len(actionHints))
	for _, hint := range actionHints {
		actions = append(actions, customActionRef{name: hint.Name})
	}
	legacyDefinitions := make([]legacyDefinitionRef, 0, len(hints))
	for _, hint := range hints {
		legacyDefinitions = append(legacyDefinitions, legacyDefinitionRef{
			name:       hint.Name,
			targetType: hint.TargetType,
		})
	}
	return customActionSource{label: label, actions: actions, legacyDefinitions: legacyDefinitions}
}

func formatNames(names []string) string {
	quoted := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		quoted = append(quoted, "`"+name+"`")
	}
	return strings.Join(quoted, ", ")
}

func formatCustomActionRefs(refs []customActionRef) string {
	names := make([]string, 0, len(refs))
	for _, ref := range refs {
		names = append(names, ref.name)
	}
	return formatNames(names)
}

func formatLegacyDefinitionRefs(refs []legacyDefinitionRef) string {
	formatted := make([]string, 0, len(refs))
	for _, ref := range refs {
		formatted = append(formatted, fmt.Sprintf("`%s` -> `%s`", ref.name, ref.targetType))
	}
	return strings.Join(formatted, ", ")
}

func buildUserCapabilities(role auth.Role) *UserCapabilities {
	if role == "" {
		return nil
	}
	return &UserCapabilities{
		Role:           role.String(),
		CanExecuteDAGs: role.CanExecute(),
		CanWriteDAGs:   role.CanWrite(),
		CanViewAudit:   role.CanManageAudit(),
		IsAdmin:        role.IsAdmin(),
	}
}

// fallbackPrompt returns a basic prompt when template execution fails.
func fallbackPrompt(env EnvironmentInfo) string {
	return "You are Dagu Assistant, an AI assistant for DAG workflows. DAGs Directory: " + env.DAGsDir
}

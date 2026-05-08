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
	StepTypes   string
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
		StepTypes:       buildStepTypesPrompt(env, p.WorkspaceAccess),
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

type customStepTypeSource struct {
	label string
	refs  []customStepTypeRef
	err   error
}

type customStepTypeRef struct {
	name       string
	targetType string
}

func buildStepTypesPrompt(env EnvironmentInfo, access *auth.WorkspaceAccess) string {
	var b strings.Builder
	b.WriteString("Available step types are generated from runtime registrations and base config files.\n")
	b.WriteString("- Builtin/runtime: ")
	b.WriteString(formatStepTypeNames(spec.StepTypeNames()))
	b.WriteString(". Omit `type` for plain command/script steps.\n")

	sources := baseConfigCustomStepTypeSources(env, access)
	if len(sources) == 0 {
		b.WriteString("- Base config custom step types: none found in configured base config files.\n")
	} else {
		b.WriteString("- Base config custom step types:\n")
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
			if len(source.refs) == 0 {
				b.WriteString("none")
				b.WriteString(".\n")
				continue
			}
			b.WriteString(formatCustomStepTypeRefs(source.refs))
			b.WriteString(".\n")
		}
	}

	b.WriteString("- Current DAG-local custom step types: inspect `step_types:` in the DAG before deciding availability.\n")
	b.WriteString("- For custom types, pass only declared `with:`/`config:` inputs; do not add executor fields hidden by the template.")
	return strings.TrimRight(b.String(), "\n")
}

func baseConfigCustomStepTypeSources(env EnvironmentInfo, access *auth.WorkspaceAccess) []customStepTypeSource {
	var sources []customStepTypeSource
	if strings.TrimSpace(env.BaseConfigFile) != "" {
		if _, err := os.Stat(env.BaseConfigFile); err == nil {
			sources = append(sources, customStepTypeSourceFromFile(
				fmt.Sprintf("global Base Config (`%s`)", env.BaseConfigFile),
				env.BaseConfigFile,
			))
		} else if !os.IsNotExist(err) {
			sources = append(sources, customStepTypeSource{
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
			sources = append(sources, customStepTypeSource{
				label: fmt.Sprintf("workspace base config directory (`%s`)", workspaceDir),
				err:   err,
			})
		}
		return sources
	}
	if !info.IsDir() {
		sources = append(sources, customStepTypeSource{
			label: fmt.Sprintf("workspace base config directory (`%s`)", workspaceDir),
			err:   fmt.Errorf("%s is not a directory", workspaceDir),
		})
		return sources
	}
	entries, err := os.ReadDir(workspaceDir)
	if err != nil {
		if !os.IsNotExist(err) {
			sources = append(sources, customStepTypeSource{
				label: fmt.Sprintf("workspace base config directory (`%s`)", workspaceDir),
				err:   err,
			})
		}
		return sources
	}
	allWorkspaces, allowedWorkspaces := allowedWorkspaceStepTypes(access)
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
			sources = append(sources, customStepTypeSource{
				label: fmt.Sprintf("workspace `%s` base config", workspaceName),
				err:   err,
			})
			continue
		}
		path := filepath.Join(workspaceDir, workspaceName, workspace.BaseConfigFileName)
		if _, err := os.Stat(path); err != nil {
			if !os.IsNotExist(err) {
				sources = append(sources, customStepTypeSource{
					label: fmt.Sprintf("workspace `%s` base config (`%s`)", workspaceName, path),
					err:   err,
				})
			}
			continue
		}
		sources = append(sources, customStepTypeSourceFromFile(
			fmt.Sprintf("workspace `%s` base config", workspaceName),
			path,
		))
	}
	return sources
}

func allowedWorkspaceStepTypes(access *auth.WorkspaceAccess) (bool, map[string]struct{}) {
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

func customStepTypeSourceFromFile(label, path string) customStepTypeSource {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return customStepTypeSource{label: label, err: err}
	}
	hints, err := spec.InheritedCustomStepTypeEditorHints(data)
	if err != nil {
		return customStepTypeSource{label: label, err: err}
	}
	refs := make([]customStepTypeRef, 0, len(hints))
	for _, hint := range hints {
		refs = append(refs, customStepTypeRef{
			name:       hint.Name,
			targetType: hint.TargetType,
		})
	}
	return customStepTypeSource{label: label, refs: refs}
}

func formatStepTypeNames(names []string) string {
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

func formatCustomStepTypeRefs(refs []customStepTypeRef) string {
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

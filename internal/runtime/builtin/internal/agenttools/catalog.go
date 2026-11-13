// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agenttools

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"

	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/llm/toolschema"
	"github.com/dagucloud/dagu/v2/internal/runtime"
)

// BuildToolRegistry builds the effective catalog of a chat step. It resolves the
// inputs that the workflow fixes and reads the tools of each MCP server. It adds
// the tool load_skill when the step has a skill. It returns nil when the step
// offers no tool and no skill.
func BuildToolRegistry(ctx context.Context, cfg *ir.LLMConfig) (*ToolRegistry, error) {
	return BuildToolRegistryWith(ctx, cfg, BuildOptions{})
}

// BuildOptions tunes the build of a catalog for a step that is not a chat step.
type BuildOptions struct {
	// DAGsPath names the key dags in an error. The default is llm.dags.
	DAGsPath string
	// ToolsPath names the key of the tool map in an error. The default is
	// llm.tools.
	ToolsPath string
	// Skip leaves out an entry of cfg.ToolEntries: the entry joins no catalog and
	// leaves no record. It is nil for a chat step.
	Skip func(ir.ToolEntry) bool
	// MCPRelay is the relay of the gateway for the named MCP entries (Spec 095, "The
	// MCP relay"). An entry with no url connects to the relay, and the coordinator
	// adds the credentials. It is nil for a step with no such entry.
	MCPRelay *MCPRelay
	// RefuseSubstitutedInputs refuses a tool DAG that puts an input of the model in
	// its command text with ${params.NAME} or ${env.NAME}. Dagu writes such a value
	// into the command, so the model would write shell code. An agent.run step sets
	// it: its child runs are trusted.
	RefuseSubstitutedInputs bool
}

// MCPRelay is the address and the secret of the relay for the named MCP entries.
type MCPRelay struct {
	// URL is the base URL of the relay: http://127.0.0.1:<port>.
	URL string
	// Secret is the secret of the relay. It goes in the header authorization. It
	// never enters an error.
	Secret string
}

// BuildToolRegistryWith is BuildToolRegistry with the options of a step that
// offers only a part of its entries to the catalog: the tool bridge of an
// agent.run step serves the DAG entries.
func BuildToolRegistryWith(ctx context.Context, cfg *ir.LLMConfig, opts BuildOptions) (*ToolRegistry, error) {
	if cfg == nil || (!cfg.HasTools() && !cfg.HasSkills()) {
		return nil, nil
	}
	dagsPath, toolsPath := opts.DAGsPath, opts.ToolsPath
	if dagsPath == "" {
		dagsPath = "llm.dags"
	}
	if toolsPath == "" {
		toolsPath = "llm.tools"
	}
	b := &catalogBuilder{
		ctx:               ctx,
		relay:             opts.MCPRelay,
		refuseSubstituted: opts.RefuseSubstitutedInputs,
		registry: &ToolRegistry{
			tools:    make(map[string]*toolInfo),
			dagNames: make(map[string]string),
		},
	}
	counts := make(map[string]int)
	for _, entry := range cfg.ToolEntries {
		category := entry.Category()
		path := EntryPath(entry, dagsPath, toolsPath, counts[category])
		counts[category]++
		if opts.Skip != nil && opts.Skip(entry) {
			continue
		}
		if err := b.add(entry, path); err != nil {
			return nil, err
		}
	}
	// The tool load_skill takes a slot of the catalog, so it joins before the
	// check of the size.
	if err := b.addSkills(cfg.Skills); err != nil {
		return nil, err
	}
	if count, limit := len(b.registry.tools), MaxTools(ctx, cfg); count > limit {
		return nil, fmt.Errorf("the effective tool catalog has %d tools, above max_tools %d", count, limit)
	}
	return b.registry, nil
}

// EntryPath returns the path of a tool entry in the step, for an error. dagsPath
// and toolsPath name the keys dags and tools. index is the place of the entry
// among the entries of its category. An entry of the list form of tools has a
// path in the key tools.
func EntryPath(entry ir.ToolEntry, dagsPath, toolsPath string, index int) string {
	switch {
	case entry.NameFromDAG:
		return fmt.Sprintf("%s[%d]", toolsPath, index)
	case entry.Kind == ir.ToolEntryDAG:
		return fmt.Sprintf("%s[%d]", dagsPath, index)
	default:
		return fmt.Sprintf("%s.%s[%d]", toolsPath, entry.Category(), index)
	}
}

type catalogBuilder struct {
	ctx               context.Context
	relay             *MCPRelay
	refuseSubstituted bool
	registry          *ToolRegistry
}

// add expands one entry.
func (b *catalogBuilder) add(entry ir.ToolEntry, path string) error {
	switch entry.Kind {
	case ir.ToolEntryDAG:
		return b.addDAG(entry, path)
	case ir.ToolEntryMCP:
		return b.addMCP(entry, path)
	default:
		return fmt.Errorf("%s: unknown tool entry kind %q", path, entry.Kind)
	}
}

func (b *catalogBuilder) addDAG(entry ir.ToolEntry, path string) error {
	name := entry.ToolName()
	if len(name) > ir.MaxToolNameLen {
		return fmt.Errorf("%s.name: the tool name %q has %d characters, above the limit of %d", path, name, len(name), ir.MaxToolNameLen)
	}
	dag, err := loadToolDAG(b.ctx, entry.DAG)
	if err != nil {
		return err
	}
	if entry.NameFromDAG && dag.Name != "" {
		// The list form of tools is deprecated. As on main, its tool name is the
		// name of the loaded DAG.
		name = dag.Name
	}
	declared, err := toolParamsForDAG(dag, entry.DAG)
	if err != nil {
		return err
	}
	fixed, visible, err := b.applyParams(entry, declared, path)
	if err != nil {
		return err
	}
	if b.refuseSubstituted {
		if err := checkSubstitutedInputs(dag, visible); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}

	description := strings.TrimSpace(dag.Description)
	if description == "" {
		description = "Run the DAG " + firstNonEmpty(entry.Name, entry.DAG) + "."
	}
	info := &toolInfo{
		Name:        name,
		Description: description,
		DAG:         dag,
		Params:      visible,
		Source:      "dag:" + firstNonEmpty(entry.Name, entry.DAG),
		Fixed:       fixed,
	}
	if err := b.register(info); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	b.registry.dagNames[name] = entry.DAG
	return nil
}

// applyParams resolves the fixed inputs of an entry and returns the inputs the
// model still sees.
func (b *catalogBuilder) applyParams(entry ir.ToolEntry, declared []toolParam, path string) (map[string]string, []toolParam, error) {
	if len(entry.Params) == 0 {
		return nil, declared, nil
	}
	known := make(map[string]struct{}, len(declared))
	for _, param := range declared {
		known[param.Name] = struct{}{}
	}

	fixed := make(map[string]string)
	descriptions := make(map[string]string)
	for _, name := range slices.Sorted(maps.Keys(entry.Params)) {
		param := entry.Params[name]
		if _, ok := known[name]; !ok {
			return nil, nil, fmt.Errorf("%s.params.%s: DAG %q declares no such input", path, name, entry.DAG)
		}
		if param.FromAI {
			if param.Description != "" {
				descriptions[name] = param.Description
			}
			continue
		}
		resolved, err := runtime.ResolveString(b.ctx, formatArgValue(param.Value), cmnvalue.WorkflowField("llm.dags.params"))
		if err != nil {
			return nil, nil, fmt.Errorf("%s.params.%s: %w", path, name, err)
		}
		fixed[name] = resolved
	}

	visible := make([]toolParam, 0, len(declared))
	for _, param := range declared {
		if _, isFixed := fixed[param.Name]; isFixed {
			continue
		}
		if description, ok := descriptions[param.Name]; ok {
			param.Description = description
		}
		visible = append(visible, param)
	}
	return fixed, visible, nil
}

func (b *catalogBuilder) addMCP(entry ir.ToolEntry, path string) error {
	source := entry.MCP
	if source == nil {
		return fmt.Errorf("%s: mcp entry has no content", path)
	}
	var endpoint string
	var headers map[string]string
	if entry.IsNamedMCP() {
		if b.relay == nil {
			return fmt.Errorf("%s: the mcp entry %q has no url and needs the relay of the gateway", path, entry.Name)
		}
		// The client connects to the relay. The coordinator holds the URL and the
		// credentials of the entry.
		endpoint = b.relay.URL + "/mcp/" + entry.Name
		headers = map[string]string{"Authorization": "Bearer " + b.relay.Secret}
	} else {
		var err error
		endpoint, err = runtime.ResolveString(b.ctx, source.URL, cmnvalue.WorkflowField("llm.tools.mcp.url"))
		if err != nil {
			return fmt.Errorf("%s.url: %w", path, err)
		}
		endpoint = strings.TrimSpace(endpoint)
		parsed, err := url.Parse(endpoint)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return fmt.Errorf("%s.url must resolve to an http or https URL", path)
		}
		headers = make(map[string]string, len(source.Headers))
		for key, value := range source.Headers {
			resolved, err := runtime.ResolveString(b.ctx, value, cmnvalue.WorkflowField("llm.tools.mcp.headers"))
			if err != nil {
				return fmt.Errorf("%s.headers.%s: %w", path, key, err)
			}
			headers[key] = resolved
		}
	}

	listed, err := listMCPTools(b.ctx, endpoint, headers)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	byName := make(map[string]int, len(listed))
	for i, tool := range listed {
		byName[tool.Name] = i
	}
	label := "mcp:" + entry.Name
	for _, name := range source.Allow {
		i, ok := byName[name]
		if !ok {
			server := redactEndpoint(endpoint)
			if entry.IsNamedMCP() {
				server = fmt.Sprintf("entry %q of the AI page", entry.Name)
			}
			return fmt.Errorf("%s.allow: server %s does not list tool %q", path, server, name)
		}
		schema, err := mcpInputSchema(listed[i].InputSchema)
		if err != nil {
			return fmt.Errorf("%s.allow: tool %q: %w", path, name, err)
		}
		toolName := mcpToolName(entry.Name, name)
		if len(toolName) > ir.MaxToolNameLen {
			return fmt.Errorf("%s.allow: the tool name %q of %s has %d characters, above the limit of %d",
				path, toolName, label, len(toolName), ir.MaxToolNameLen)
		}
		if err := b.register(&toolInfo{
			Name:        toolName,
			Description: listed[i].Description,
			Schema:      schema,
			Source:      label,
			MCP: &mcpBinding{
				endpoint: endpoint,
				headers:  headers,
				tool:     name,
			},
		}); err != nil {
			return fmt.Errorf("%s.allow: %w", path, err)
		}
	}
	return nil
}

// mcpToolName returns the tool name of a server tool: <label>_<tool>. It is the
// same name that a CLI agent reports for the tool, after Dagu normalizes it.
func mcpToolName(label, tool string) string {
	return label + "_" + ir.ToolNameFor(tool)
}

// mcpInputSchema returns the input schema of an MCP tool as a map. A server
// that gives none gets an empty object schema.
func mcpInputSchema(schema any) (map[string]any, error) {
	if schema == nil {
		return toolschema.Build(nil), nil
	}
	data, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("encode input schema: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("input schema is not an object: %w", err)
	}
	if len(out) == 0 {
		return toolschema.Build(nil), nil
	}
	return out, nil
}

// register adds a tool to the catalog. A tool name that another tool of the
// catalog has is an error that names both sources.
func (b *catalogBuilder) register(info *toolInfo) error {
	if other, taken := b.registry.tools[info.Name]; taken {
		return fmt.Errorf("tool name %q of %s is also the tool name of %s", info.Name, info.Source, other.Source)
	}
	b.registry.tools[info.Name] = info
	b.registry.order = append(b.registry.order, info.Name)
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

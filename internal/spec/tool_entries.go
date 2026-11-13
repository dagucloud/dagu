// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/aiendpoint"
	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// Spec 090 defines the tools of a step. The key dags holds the DAG tools. The
// key tools is a map with the category mcp. Spec 091 adds the category builtin on
// an agent.run step.

// Categories of the tool entries. The entries of dags are in the key dags. The
// other categories are keys of the tool map.
const (
	toolCategoryDAGs    = "dags"
	toolCategoryMCP     = "mcp"
	toolCategoryBuiltin = "builtin"
)

// toolEntryKeys lists the keys of an entry for each category.
var toolEntryKeys = map[string][]string{
	toolCategoryDAGs: {"name", "params"},
	toolCategoryMCP:  {"name", "url", "headers", "allow"},
}

// builtinToolKeys lists the keys of the category builtin.
var builtinToolKeys = []string{"read", "write", "edit", "search", "shell"}

// mcpServerNameRegexp is the rule for the name of an mcp entry.
var mcpServerNameRegexp = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// namedMCPNameRegexp is the rule for the name of an mcp entry with no url: the
// rule of the name of a MCP server entry of the AI page.
var namedMCPNameRegexp = regexp.MustCompile(aiendpoint.MCPNamePattern)

// bridgeServerName is the name of the MCP server of the tool bridge in the
// config of the CLI (Spec 091, "Tool bridge"). An mcp entry cannot use it.
const bridgeServerName = aiendpoint.ReservedMCPName

// toolMapEntry is one entry of the key dags or of a list category of the tool
// map, with the path of the key as the author wrote it. An error names this
// path.
type toolMapEntry struct {
	entry    ir.ToolEntry
	category string
	path     string
}

// toolMap holds the parsed tools of a step. entries holds the entries of dags
// and then the entries of mcp, each in the order written.
type toolMap struct {
	entries []toolMapEntry
	// builtin is the category builtin of an agent.run step. It is nil when the
	// tool map has no such category.
	builtin *ir.BuiltinTools
}

// isEmpty reports whether the map offers nothing.
func (tm *toolMap) isEmpty() bool {
	return tm == nil || (len(tm.entries) == 0 && tm.builtin == nil)
}

// parseToolMapValue parses the keys dags and tools of a chat.completion step or
// of an llm block. prefix is the path of the block. It returns nil when no key
// is set. The key tools holds the map form, or the list form of main.
func parseToolMapValue(rawDAGs, rawTools any, prefix string) (*toolMap, error) {
	if rawDAGs == nil && rawTools == nil {
		return nil, nil
	}
	dagsPath, toolsPath := prefix+"."+toolCategoryDAGs, prefix+".tools"
	if list, isList := rawTools.([]any); isList {
		if rawDAGs != nil {
			return nil, ir.NewValidationError(dagsPath, nil,
				fmt.Errorf("key %q and key %q contradict each other; the list form of tools already lists the DAG tools", "dags", "tools"))
		}
		return parseToolList(list, toolsPath)
	}
	tm := &toolMap{}
	if rawDAGs != nil {
		if err := tm.addDAGs(rawDAGs, dagsPath); err != nil {
			return nil, err
		}
	}
	if rawTools != nil {
		if err := tm.addTools(rawTools, toolsPath, false); err != nil {
			return nil, err
		}
	}
	return tm, nil
}

// parseToolList parses the list form of tools: a list of DAG names, as on main.
// The list form is DEPRECATED: use the key dags. Each name is an entry of dags
// with no params, and the tool name is the name of the loaded DAG, as on main.
func parseToolList(list []any, path string) (*toolMap, error) {
	tm := &toolMap{}
	for i, item := range list {
		entryPath := fmt.Sprintf("%s[%d]", path, i)
		name, ok := item.(string)
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			return nil, ir.NewValidationError(entryPath, item,
				fmt.Errorf("an entry of the list form of tools must be the name of a DAG, got %s; the list form is deprecated, use the key dags", describeYAMLKind(item)))
		}
		tm.entries = append(tm.entries, toolMapEntry{
			entry:    ir.ToolEntry{Kind: ir.ToolEntryDAG, Name: name, DAG: name, NameFromDAG: true},
			category: toolCategoryDAGs,
			path:     entryPath,
		})
	}
	return tm, nil
}

// parseAgentTools parses the keys dags and tools of the with of an agent.run
// step. label is the path of the with. The key tools takes the map form only:
// the list form of main is valid on chat.completion only.
func parseAgentTools(with map[string]any, label string) (*toolMap, error) {
	tm := &toolMap{}
	if raw, present := with[toolCategoryDAGs]; present {
		if err := tm.addDAGs(raw, label+"."+toolCategoryDAGs); err != nil {
			return nil, err
		}
	}
	if raw, present := with["tools"]; present {
		path := label + ".tools"
		if _, isList := raw.([]any); isList {
			return nil, ir.NewValidationError(path, raw,
				fmt.Errorf("the list form of tools is valid on chat.completion only, and it is deprecated; an agent.run step lists its DAG tools in the key dags"))
		}
		if err := tm.addTools(raw, path, true); err != nil {
			return nil, err
		}
	}
	return tm, nil
}

// addDAGs adds the entries of the key dags. The duplicate names are checked by
// check, once the map is complete.
func (tm *toolMap) addDAGs(raw any, path string) error {
	entries, err := parseEntryList(toolCategoryDAGs, raw, path)
	if err != nil {
		return err
	}
	tm.entries = append(tm.entries, entries...)
	return nil
}

// addTools checks the closed schema of the tool map at path, and adds its
// entries. allowBuiltin is true on an agent.run step, where the category builtin
// is valid. The key tools holds no DAG tool: the key dags holds them.
func (tm *toolMap) addTools(raw any, path string, allowBuiltin bool) error {
	categories := []string{toolCategoryMCP}
	if allowBuiltin {
		categories = append(categories, toolCategoryBuiltin)
	}
	object, ok := raw.(map[string]any)
	if !ok {
		return ir.NewValidationError(path, raw,
			fmt.Errorf("tools must be a map with the categories %s, got %s", strings.Join(categories, ", "), describeYAMLKind(raw)))
	}
	if builtin, present := object[toolCategoryBuiltin]; present && !allowBuiltin {
		return ir.NewValidationError(path+"."+toolCategoryBuiltin, builtin,
			fmt.Errorf("the category builtin is valid only on an agent.run step with a CLI provider; a chat.completion step has no built-in tool"))
	}
	if err := checkClosedKeys(path, object, "the tool map", categories); err != nil {
		return err
	}

	if rawList, present := object[toolCategoryMCP]; present {
		entries, err := parseEntryList(toolCategoryMCP, rawList, path+"."+toolCategoryMCP)
		if err != nil {
			return err
		}
		tm.entries = append(tm.entries, entries...)
	}
	if rawBuiltin, present := object[toolCategoryBuiltin]; present {
		builtin, err := parseBuiltinTools(rawBuiltin, path+"."+toolCategoryBuiltin)
		if err != nil {
			return err
		}
		tm.builtin = builtin
	}
	return nil
}

// parseEntryList parses the list of entries of a category at path.
func parseEntryList(category string, raw any, path string) ([]toolMapEntry, error) {
	list, ok := raw.([]any)
	if !ok {
		return nil, ir.NewValidationError(path, raw,
			fmt.Errorf("%s must be a list of entries, got %s", category, describeYAMLKind(raw)))
	}
	entries := make([]toolMapEntry, 0, len(list))
	for i, item := range list {
		entryPath := fmt.Sprintf("%s[%d]", path, i)
		entry, err := parseToolMapEntry(category, item, entryPath)
		if err != nil {
			return nil, err
		}
		entries = append(entries, toolMapEntry{entry: *entry, category: category, path: entryPath})
	}
	return entries, nil
}

// checkClosedKeys checks the keys of a closed block at path. The error names the
// first unknown key in sorted order, the block and the valid keys.
func checkClosedKeys(path string, raw map[string]any, block string, valid []string) error {
	for _, key := range sortedKeys(raw) {
		if !slices.Contains(valid, key) {
			return ir.NewValidationError(path+"."+key, raw[key],
				fmt.Errorf("unknown key %q in %s; valid keys are %s", key, block, strings.Join(valid, ", ")))
		}
	}
	return nil
}

// describeYAMLKind names the kind of a YAML value for an error.
func describeYAMLKind(value any) string {
	switch value.(type) {
	case nil:
		return "no value"
	case string:
		return "a string"
	case []any:
		return "a list"
	case map[string]any:
		return "a map"
	case bool:
		return "a boolean"
	default:
		return fmt.Sprintf("%T", value)
	}
}

// parseToolMapEntry reads one entry of the key dags or of the category mcp.
func parseToolMapEntry(category string, item any, path string) (*ir.ToolEntry, error) {
	raw, ok := item.(map[string]any)
	if !ok {
		return nil, ir.NewValidationError(path, item,
			fmt.Errorf("an entry of %s must be an object with the key name, got %s", category, describeYAMLKind(item)))
	}
	if err := checkClosedKeys(path, raw, "an entry of "+category, toolEntryKeys[category]); err != nil {
		return nil, err
	}

	rawName, present := raw["name"]
	name, isString := rawName.(string)
	name = strings.TrimSpace(name)
	if !present || !isString || name == "" {
		return nil, ir.NewValidationError(path+".name", rawName, fmt.Errorf("name is required and must be a non-empty string"))
	}

	entry := &ir.ToolEntry{Name: name}

	switch category {
	case toolCategoryDAGs:
		entry.Kind = ir.ToolEntryDAG
		entry.DAG = name
	default:
		entry.Kind = ir.ToolEntryMCP
		if err := parseMCPEntry(entry, raw, path); err != nil {
			return nil, err
		}
		return entry, nil
	}
	if rawParams, present := raw["params"]; present {
		params, err := parseToolParams(rawParams, path+".params")
		if err != nil {
			return nil, err
		}
		entry.Params = params
	}
	return entry, nil
}

// parseBuiltinTools reads the category builtin: the neutral choice of the
// built-in tools. The keys read, write, edit and search take the value true.
// The key shell takes an object with allow and deny.
func parseBuiltinTools(raw any, path string) (*ir.BuiltinTools, error) {
	object, ok := raw.(map[string]any)
	if !ok {
		return nil, ir.NewValidationError(path, raw,
			fmt.Errorf("builtin must be an object with the keys %s", strings.Join(builtinToolKeys, ", ")))
	}
	if err := checkClosedKeys(path, object, "builtin", builtinToolKeys); err != nil {
		return nil, err
	}
	tools := &ir.BuiltinTools{}
	for _, key := range sortedKeys(object) {
		keyPath := path + "." + key
		value := object[key]
		switch key {
		case "read", "write", "edit", "search":
			if on, ok := value.(bool); !ok || !on {
				return nil, ir.NewValidationError(keyPath, value, fmt.Errorf("%s must be true; omit the key to leave the tool out", key))
			}
			switch key {
			case "read":
				tools.Read = true
			case "write":
				tools.Write = true
			case "edit":
				tools.Edit = true
			default:
				tools.Search = true
			}
		default:
			shell, err := parseBuiltinShell(value, keyPath)
			if err != nil {
				return nil, err
			}
			tools.Shell = shell
		}
	}
	if !tools.Read && !tools.Write && !tools.Edit && !tools.Search && tools.Shell == nil {
		return nil, ir.NewValidationError(path, raw, fmt.Errorf("builtin selects no tool"))
	}
	return tools, nil
}

// parseBuiltinShell reads the value of the shell key of the category builtin.
func parseBuiltinShell(value any, path string) (*ir.BuiltinShell, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, ir.NewValidationError(path, value,
			fmt.Errorf("shell must be an object with allow and deny; the value true is not valid because it allows every command"))
	}
	if err := checkClosedKeys(path, object, "shell", []string{"allow", "deny"}); err != nil {
		return nil, err
	}
	shell := &ir.BuiltinShell{}
	for _, key := range []string{"allow", "deny"} {
		raw, present := object[key]
		if !present {
			continue
		}
		list, ok := raw.([]any)
		if !ok {
			return nil, ir.NewValidationError(path+"."+key, raw, fmt.Errorf("%s must be a list of command patterns", key))
		}
		patterns := make([]string, 0, len(list))
		for i, item := range list {
			pattern, ok := item.(string)
			if !ok || strings.TrimSpace(pattern) == "" || strings.TrimSpace(pattern) == "*" {
				return nil, ir.NewValidationError(fmt.Sprintf("%s.%s[%d]", path, key, i), item,
					fmt.Errorf("a command pattern must be a command prefix; an empty pattern and * are not valid"))
			}
			patterns = append(patterns, strings.TrimSpace(pattern))
		}
		if key == "allow" {
			shell.Allow = patterns
		} else {
			shell.Deny = patterns
		}
	}
	if len(shell.Allow) == 0 {
		return nil, ir.NewValidationError(path+".allow", object["allow"],
			fmt.Errorf("shell needs allow, a non-empty list of command patterns"))
	}
	return shell, nil
}

func parseToolParams(raw any, path string) (map[string]ir.ToolParam, error) {
	values, ok := raw.(map[string]any)
	if !ok {
		return nil, ir.NewValidationError(path, raw, fmt.Errorf("params must be a map from an input name to a value"))
	}
	params := make(map[string]ir.ToolParam, len(values))
	for _, name := range sortedKeys(values) {
		value := values[name]
		paramPath := path + "." + name
		if strings.TrimSpace(name) == "" {
			return nil, ir.NewValidationError(path, raw, fmt.Errorf("an input name must not be empty"))
		}
		if object, ok := value.(map[string]any); ok {
			if fromAI, isAI := object["from_ai"]; isAI {
				if err := checkClosedKeys(paramPath, object, "an input with from_ai", []string{"from_ai"}); err != nil {
					return nil, err
				}
				param, err := parseFromAI(fromAI, paramPath+".from_ai")
				if err != nil {
					return nil, err
				}
				params[name] = param
				continue
			}
		}
		params[name] = ir.ToolParam{Value: value}
	}
	return params, nil
}

func parseFromAI(raw any, path string) (ir.ToolParam, error) {
	switch v := raw.(type) {
	case bool:
		if !v {
			return ir.ToolParam{}, ir.NewValidationError(path, raw,
				fmt.Errorf("from_ai must be true or an object; omit the input to leave it to the model"))
		}
		return ir.ToolParam{FromAI: true}, nil
	case map[string]any:
		param := ir.ToolParam{FromAI: true}
		if err := checkClosedKeys(path, v, "from_ai", []string{"description"}); err != nil {
			return ir.ToolParam{}, err
		}
		for _, value := range v {
			text, ok := value.(string)
			if !ok || strings.TrimSpace(text) == "" {
				return ir.ToolParam{}, ir.NewValidationError(path+".description", value,
					fmt.Errorf("description must be a non-empty string"))
			}
			param.Description = strings.TrimSpace(text)
		}
		return param, nil
	default:
		return ir.ToolParam{}, ir.NewValidationError(path, raw, fmt.Errorf("from_ai must be true or an object"))
	}
}

// parseMCPEntry reads the keys url, headers and allow of an mcp entry. The
// name is already read. An entry with url is inline: the CLI or the step connects
// to the server. An entry with no url names a MCP server entry of the AI page
// (Spec 095, "The key tools.mcp"). It has no headers, because the entry holds the
// credentials.
func parseMCPEntry(entry *ir.ToolEntry, raw map[string]any, path string) error {
	if !mcpServerNameRegexp.MatchString(entry.Name) {
		return ir.NewValidationError(path+".name", entry.Name,
			fmt.Errorf("name of an mcp entry must match %s", mcpServerNameRegexp.String()))
	}
	source := &ir.MCPToolSource{}

	if rawURL, present := raw["url"]; !present {
		if !namedMCPNameRegexp.MatchString(entry.Name) {
			return ir.NewValidationError(path+".name", entry.Name,
				fmt.Errorf("name of an mcp entry with no url must match %s, the rule of a MCP server entry of the AI page", aiendpoint.MCPNamePattern))
		}
		if _, hasHeaders := raw["headers"]; hasHeaders {
			return ir.NewValidationError(path+".headers", raw["headers"],
				fmt.Errorf("headers is not valid on an mcp entry with no url; the MCP server entry %q of the AI page holds its credentials, so remove headers, or write the key url for an inline entry", entry.Name))
		}
	} else {
		text, ok := rawURL.(string)
		if !ok || strings.TrimSpace(text) == "" {
			return ir.NewValidationError(path+".url", rawURL,
				fmt.Errorf("url must be a non-empty string; omit it to use the MCP server entry %q of the AI page", entry.Name))
		}
		text = strings.TrimSpace(text)
		// A value reference only has a final value at run time.
		if !cmnvalue.HasAnyReference(text) && !strings.Contains(text, "`") {
			parsed, err := url.Parse(text)
			if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
				return ir.NewValidationError(path+".url", text,
					fmt.Errorf("url must be an http or https URL"))
			}
		}
		source.URL = text
	}

	if rawHeaders, present := raw["headers"]; present {
		headers, ok := rawHeaders.(map[string]any)
		if !ok {
			return ir.NewValidationError(path+".headers", rawHeaders, fmt.Errorf("headers must be a map of strings"))
		}
		source.Headers = make(map[string]string, len(headers))
		for _, key := range sortedKeys(headers) {
			value := headers[key]
			text, ok := value.(string)
			if !ok {
				return ir.NewValidationError(path+".headers."+key, value, fmt.Errorf("a header value must be a string"))
			}
			source.Headers[key] = text
		}
	}

	allowRaw, ok := raw["allow"].([]any)
	if !ok || len(allowRaw) == 0 {
		return ir.NewValidationError(path+".allow", raw["allow"],
			fmt.Errorf("allow is required and lists the server tools to offer"))
	}
	for i, item := range allowRaw {
		name, ok := item.(string)
		if !ok || strings.TrimSpace(name) == "" {
			return ir.NewValidationError(fmt.Sprintf("%s.allow[%d]", path, i), item,
				fmt.Errorf("a tool name must be a non-empty string"))
		}
		source.Allow = append(source.Allow, strings.TrimSpace(name))
	}
	entry.MCP = source
	return nil
}

// check applies the rules of the tool names to the map: a tool name above 64
// characters, two entries of dags with the same tool name, two mcp
// entries with the same name, and an mcp entry named dagu. The error names the
// path of the entry that the author wrote.
func (tm *toolMap) check() error {
	if tm == nil {
		return nil
	}
	toolNames := make(map[string]string)
	mcpNames := make(map[string]string)
	for _, e := range tm.entries {
		switch e.category {
		case toolCategoryMCP:
			name := e.entry.Name
			if name == bridgeServerName {
				return ir.NewValidationError(e.path+".name", name,
					fmt.Errorf("the name %q is the name of the tool bridge, which serves the Dagu tools of an agent.run step; use another name", bridgeServerName))
			}
			if first, dup := mcpNames[name]; dup {
				return ir.NewValidationError(e.path+".name", name,
					fmt.Errorf("mcp name %q is also the name of %s; the names must be unique", name, first))
			}
			mcpNames[name] = e.path
		default:
			name := e.entry.ToolName()
			if len(name) > ir.MaxToolNameLen {
				return ir.NewValidationError(e.path+".name", e.entry.Name,
					fmt.Errorf("the tool name %q has %d characters, above the limit of %d", name, len(name), ir.MaxToolNameLen))
			}
			if first, dup := toolNames[name]; dup {
				return ir.NewValidationError(e.path, e.entry.Name,
					fmt.Errorf("tool name %q is also the tool name of %s", name, first))
			}
			toolNames[name] = e.path
		}
	}
	return nil
}

// irEntries returns the entries as the IR keeps them: the entries of dags, the
// entries of mcp, then the builtin entry.
func (tm *toolMap) irEntries() []ir.ToolEntry {
	if tm.isEmpty() {
		return nil
	}
	entries := make([]ir.ToolEntry, 0, len(tm.entries)+1)
	for _, e := range tm.entries {
		entries = append(entries, e.entry)
	}
	if tm.builtin != nil {
		entries = append(entries, ir.ToolEntry{Kind: ir.ToolEntryBuiltin, Builtin: tm.builtin})
	}
	return entries
}

// buildToolEntries returns the tool entries of a step or an llm block, checked.
func buildToolEntries(tools *toolMap) ([]ir.ToolEntry, error) {
	if err := tools.check(); err != nil {
		return nil, err
	}
	return tools.irEntries(), nil
}

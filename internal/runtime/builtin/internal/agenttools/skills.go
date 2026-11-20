// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agenttools

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/ir"
	llmpkg "github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/llm/toolschema"
	"github.com/dagucloud/dagu/v2/internal/skill"
)

// LoadSkillToolName is the name of the tool that returns the instructions of
// a skill. A tool of the map cannot have this name (Spec 090).
const LoadSkillToolName = "load_skill"

// ReadSkillFileToolName is the name of the tool that returns a file of the
// folders references/ or scripts/ of a skill (Spec 096). A tool of the map
// cannot have this name.
const ReadSkillFileToolName = "read_skill_file"

// maxListedSkillFiles bounds the list of files that load_skill returns.
const maxListedSkillFiles = 100

// skillToolSource is the source of the tool load_skill in the catalog record.
const skillToolSource = "skills"

// SkillSet is the list of skills of a step. Each skill is loadable: the tool
// load_skill serves it to a chat.completion step, and Dagu mounts it as a file
// for an agent.run step.
type SkillSet struct {
	// toolName is the name of the load_skill tool in the catalog. It is empty
	// when the set belongs to no catalog.
	toolName string
	// skills holds the skills, in order.
	skills []ir.SkillEntry
}

// skillBinding is the data of the tools load_skill and read_skill_file.
type skillBinding struct {
	skills map[string]ir.SkillEntry
	names  []string
	// mu guards loaded: the tool calls of one turn can run together.
	mu     sync.Mutex
	loaded map[string]bool
}

// NewSkillSet returns the set of the skills of a step. It is nil when the step
// has no skill.
func NewSkillSet(skills []ir.SkillEntry) *SkillSet {
	if len(skills) == 0 {
		return nil
	}
	return &SkillSet{skills: skills}
}

// Loadable returns the skills, in order. The tool load_skill serves them; a
// CLI agent finds them as files.
func (s *SkillSet) Loadable() []ir.SkillEntry {
	if s == nil {
		return nil
	}
	return s.skills
}

// addSkills puts the tool load_skill into the catalog when the step has a
// skill. It adds the tool read_skill_file when a skill has a file that the
// model can read.
func (b *catalogBuilder) addSkills(skills []ir.SkillEntry) error {
	set := NewSkillSet(skills)
	if set == nil {
		return nil
	}
	b.registry.skills = set

	binding := &skillBinding{skills: make(map[string]ir.SkillEntry, len(skills)), loaded: map[string]bool{}}
	choices := make([]any, 0, len(skills))
	hasFiles := false
	for _, entry := range skills {
		binding.skills[entry.Name] = entry
		binding.names = append(binding.names, entry.Name)
		choices = append(choices, entry.Name)
		if len(readableFiles(entry)) > 0 {
			hasFiles = true
		}
	}
	set.toolName = LoadSkillToolName
	if err := b.register(&toolInfo{
		Name:        set.toolName,
		Description: "Return the instructions of one skill. Use it when a skill of the Skills section fits the task.",
		Schema: toolschema.Build([]toolParam{{
			Name:        "name",
			Type:        "string",
			Description: "Name of the skill to load.",
			Required:    true,
			Enum:        choices,
		}}),
		Source: skillToolSource,
		Skills: binding,
	}); err != nil {
		return err
	}
	if !hasFiles {
		return nil
	}
	return b.register(&toolInfo{
		Name:        ReadSkillFileToolName,
		Description: "Return one text file of the folder references/ or scripts/ of a skill. The skill lists its files when you load it.",
		Schema: toolschema.Build([]toolParam{
			{Name: "name", Type: "string", Description: "Name of the skill.", Required: true, Enum: choices},
			{Name: "path", Type: "string", Description: "Path of the file, relative to the folder of the skill, for example references/guide.md.", Required: true},
		}),
		Source: skillToolSource,
		Skills: binding,
	})
}

// readableFiles returns the files of a skill that read_skill_file serves: the
// text files of references/ and scripts/, sorted by path.
func readableFiles(entry ir.SkillEntry) []ir.SkillFile {
	var files []ir.SkillFile
	for _, f := range entry.Files {
		if f.Text != nil && skill.IsReadable(f.Path) {
			files = append(files, f)
		}
	}
	return files
}

// SystemText returns the section Skills of the system message: the list of the
// skills and the tool that loads them. It is empty when the step has no skill.
func (s *SkillSet) SystemText() string {
	if s == nil || len(s.skills) == 0 {
		return ""
	}
	var list strings.Builder
	fmt.Fprintf(&list, "## Skills\n\nThese skills hold instructions. Call the tool `%s` with a skill name to read the instructions of that skill.\n\n", s.toolName)
	for i, entry := range s.skills {
		if i > 0 {
			list.WriteString("\n")
		}
		fmt.Fprintf(&list, "- %s: %s", entry.Name, entry.Description)
	}
	return list.String()
}

// Apply adds the skill sections to the first system message of msgs, or puts a
// system message first when there is none. The caller owns msgs.
func (s *SkillSet) Apply(msgs []llmpkg.Message) []llmpkg.Message {
	text := s.SystemText()
	if text == "" {
		return msgs
	}
	for i, msg := range msgs {
		if msg.Role == llmpkg.RoleSystem {
			msgs[i].Content = strings.TrimRight(msg.Content, "\n") + "\n\n" + text
			return msgs
		}
	}
	return append([]llmpkg.Message{{Role: llmpkg.RoleSystem, Content: text}}, msgs...)
}

// Definitions returns the record of the skills of the step, for the run.
func (s *SkillSet) Definitions() []ir.SkillDefinition {
	if s == nil || len(s.skills) == 0 {
		return nil
	}
	definitions := make([]ir.SkillDefinition, 0, len(s.skills))
	for _, entry := range s.skills {
		definitions = append(definitions, ir.SkillDefinition{Name: entry.Name, Description: entry.Description, Hash: entry.Hash})
	}
	return definitions
}

// executeSkillToolCall runs load_skill or read_skill_file.
func (e *ToolExecutor) executeSkillToolCall(tc llmpkg.ToolCall, info *toolInfo) ir.ToolResult {
	result := ir.ToolResult{ToolCallID: tc.ID, Name: info.Name}
	var args struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	if tc.Function.Arguments != "" {
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			result.Error = fmt.Sprintf("failed to parse arguments: %v", err)
			return result
		}
	}
	binding := info.Skills
	entry, ok := binding.skills[args.Name]
	if !ok {
		result.Error = fmt.Sprintf("skill %q is not available; available skills: %s",
			args.Name, strings.Join(binding.names, ", "))
		return result
	}
	if info.Name == ReadSkillFileToolName {
		result.Content, result.Error = readSkillFile(entry, args.Path)
		return result
	}
	result.Content = binding.load(entry)
	return result
}

// load returns the result of load_skill: the body in the tags of the Agent
// Skills client guide, then the list of the files that read_skill_file serves.
// A second call for the same skill returns a short note: the body is already in
// the conversation.
func (b *skillBinding) load(entry ir.SkillEntry) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.loaded[entry.Name] {
		return fmt.Sprintf("The skill %q is already loaded. Its instructions are earlier in this conversation.", entry.Name)
	}
	b.loaded[entry.Name] = true

	var out strings.Builder
	fmt.Fprintf(&out, "<skill_content name=%q>\n%s", entry.Name, strings.TrimRight(entry.Body, "\n"))
	files := readableFiles(entry)
	if len(files) > 0 {
		out.WriteString("\n\nRelative paths in this skill are relative to the folder of the skill. ")
		fmt.Fprintf(&out, "Read a file with the tool `%s`.\n<skill_resources>", ReadSkillFileToolName)
		for i, f := range files {
			if i == maxListedSkillFiles {
				fmt.Fprintf(&out, "\n  <!-- %d more files not listed -->", len(files)-i)
				break
			}
			fmt.Fprintf(&out, "\n  <file>%s</file>", f.Path)
		}
		out.WriteString("\n</skill_resources>")
	}
	out.WriteString("\n</skill_content>")
	return out.String()
}

// readSkillFile returns the content of a file of references/ or scripts/. It
// returns an error text for any other path: a path outside the skill, an asset
// or a file that is too large.
func readSkillFile(entry ir.SkillEntry, rel string) (content, errText string) {
	for _, f := range entry.Files {
		if f.Path != rel {
			continue
		}
		switch {
		case !skill.IsReadable(f.Path):
			return "", fmt.Sprintf("the file %q of the skill %q is not a text file of references/ or scripts/; the model cannot read it", rel, entry.Name)
		case f.Text == nil:
			return "", fmt.Sprintf("the file %q of the skill %q is not available as text", rel, entry.Name)
		case len(*f.Text) > skill.MaxReadFileBytes:
			return "", fmt.Sprintf("the file %q is %d bytes; the limit of read_skill_file is %d bytes", rel, len(*f.Text), skill.MaxReadFileBytes)
		}
		return *f.Text, ""
	}
	files := readableFiles(entry)
	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	if len(paths) > maxListedSkillFiles {
		paths = paths[:maxListedSkillFiles]
	}
	return "", fmt.Sprintf("the skill %q has no file %q; the files that you can read: %s", entry.Name, rel, strings.Join(paths, ", "))
}

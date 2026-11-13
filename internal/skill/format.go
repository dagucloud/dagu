// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package skill

import (
	"bytes"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/goccy/go-yaml"
)

// allowedKeys are the keys that the front matter can hold. Every other key,
// including the extension keys of Claude Code, is an error.
var allowedKeys = []string{"name", "description", "license", "compatibility", "metadata", "allowed-tools"}

var xmlTagPattern = regexp.MustCompile(`</?[A-Za-z][^<>]*>`)

// NamePattern describes the rule of a name for a message.
const NamePattern = "1 to 64 characters: lower case letters, digits and single hyphens, no hyphen at the start or at the end"

// CheckName checks a name against the name rule of the format. It returns one
// issue for each rule that the name breaks. The name of the folder uses the
// same rule.
func CheckName(name, file string) []Issue {
	var issues []Issue
	add := func(rule, format string, args ...any) {
		issues = append(issues, Issue{Severity: SeverityError, Rule: rule, File: file, Message: fmt.Sprintf(format, args...)})
	}
	n := utf8.RuneCountInString(name)
	if n < 1 || n > MaxNameLength {
		add(RuleNameLength, "the name %q has %d characters; it must have 1 to %d", name, n, MaxNameLength)
	}
	for _, r := range name {
		if r == '-' || unicode.IsDigit(r) || (unicode.IsLetter(r) && unicode.IsLower(r)) {
			continue
		}
		add(RuleNameCharset, "the name %q holds the character %q; only lower case letters, digits and - are allowed", name, r)
		break
	}
	if strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") {
		add(RuleNameHyphenEdge, "the name %q starts or ends with a hyphen", name)
	}
	if strings.Contains(name, "--") {
		add(RuleNameHyphenDouble, "the name %q holds two hyphens in a row", name)
	}
	return issues
}

// IsSafeFolderName reports whether the name can be the name of a folder of the
// skills directory. It rejects path separators, hidden names and control
// characters. It does not apply the name rule: a folder that breaks the rule is
// listed with an error.
func IsSafeFolderName(name string) bool {
	if name == "" || len(name) > 255 || !utf8.ValidString(name) || strings.HasPrefix(name, ".") {
		return false
	}
	for _, r := range name {
		if r == '/' || r == '\\' || r == 0 || unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// splitFrontMatter cuts SKILL.md into the YAML between the first two --- lines
// and the text after the second one. The file MUST start with a --- line.
func splitFrontMatter(data []byte) (front []byte, body string, issue *Issue) {
	text := bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	const fence = "---\n"
	if !bytes.HasPrefix(text, []byte(fence)) {
		return nil, "", &Issue{Severity: SeverityError, Rule: RuleFrontMatterStart, File: FileName,
			Message: "the file must start with a front matter block that begins with a --- line"}
	}
	rest := text[len(fence):]
	offset := 0
	for offset <= len(rest) {
		end := bytes.IndexByte(rest[offset:], '\n')
		var line []byte
		if end < 0 {
			line = rest[offset:]
		} else {
			line = rest[offset : offset+end]
		}
		if string(bytes.TrimRight(line, " \t")) == "---" {
			after := rest[min(offset+len(line)+1, len(rest)):]
			return rest[:offset], strings.TrimLeft(string(after), "\n"), nil
		}
		if end < 0 {
			break
		}
		offset += end + 1
	}
	return nil, "", &Issue{Severity: SeverityError, Rule: RuleFrontMatterEnd, File: FileName,
		Message: "the front matter block has no closing --- line"}
}

// CheckSkillFile checks the content of SKILL.md of the folder name. It returns
// the front matter, the body and the issues of the file. The front matter
// values are set even when a rule fails, as far as they can be read.
func CheckSkillFile(name string, data []byte) (FrontMatter, string, []Issue) {
	var issues []Issue
	errorf := func(rule, format string, args ...any) {
		issues = append(issues, Issue{Severity: SeverityError, Rule: rule, File: FileName, Message: fmt.Sprintf(format, args...)})
	}
	warnf := func(rule, format string, args ...any) {
		issues = append(issues, Issue{Severity: SeverityWarning, Rule: rule, File: FileName, Message: fmt.Sprintf(format, args...)})
	}

	if !utf8.Valid(data) {
		errorf(RuleNotUTF8, "the file is not valid UTF-8 text")
		return FrontMatter{}, "", issues
	}
	front, body, issue := splitFrontMatter(data)
	if issue != nil {
		return FrontMatter{}, "", []Issue{*issue}
	}

	var raw map[string]any
	if err := yaml.UnmarshalWithOptions(front, &raw, yaml.Strict()); err != nil {
		rule := RuleFrontMatterYAML
		if strings.Contains(err.Error(), "already defined") {
			rule = RuleFrontMatterDup
		}
		errorf(rule, "the front matter is not valid YAML or repeats a key: %s", firstLine(err.Error()))
		return FrontMatter{}, body, issues
	}

	var meta FrontMatter
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !isAllowedKey(k) {
			errorf(RuleFrontMatterKey, "the front matter key %q is not allowed; the keys are %s. Put custom data in metadata", k, strings.Join(allowedKeys, ", "))
		}
	}

	str := func(key string) (string, bool, bool) { // value, present, ok
		v, present := raw[key]
		if !present {
			return "", false, true
		}
		s, ok := v.(string)
		return s, true, ok
	}

	// name
	if s, present, ok := str("name"); !present {
		errorf(RuleNameMissing, "the front matter key name is required")
	} else if !ok {
		errorf(RuleNameType, "the front matter key name must be a string")
	} else {
		meta.Name = s
		if s != name {
			errorf(RuleNameFolder, "the front matter name is %q, but the folder name is %q; they must be equal", s, name)
		}
		issues = append(issues, CheckName(s, FileName)...)
	}

	// description
	if s, present, ok := str("description"); !present {
		errorf(RuleDescriptionMiss, "the front matter key description is required")
	} else if !ok {
		errorf(RuleDescriptionType, "the front matter key description must be a string")
	} else {
		meta.Description = strings.Join(strings.Fields(s), " ")
		n := utf8.RuneCountInString(meta.Description)
		switch {
		case n == 0:
			errorf(RuleDescriptionMiss, "the description must not be empty")
		case n > MaxDescriptionLength:
			errorf(RuleDescriptionLength, "the description has %d characters; the limit is %d", n, MaxDescriptionLength)
		}
		if xmlTagPattern.MatchString(meta.Description) {
			errorf(RuleDescriptionXML, "the description must not contain XML tags")
		}
		if n > 0 && n < WarnDescriptionLength {
			warnf(RuleDescriptionShort, "the description has %d characters; say what the skill does and when to use it", n)
		}
	}

	// license
	if s, present, ok := str("license"); present {
		s = strings.TrimSpace(s)
		switch {
		case !ok:
			errorf(RuleLicense, "the front matter key license must be a string")
		case s == "" || utf8.RuneCountInString(s) > MaxLicenseLength:
			errorf(RuleLicense, "the license must have 1 to %d characters", MaxLicenseLength)
		default:
			meta.License = s
		}
	}

	// compatibility
	if s, present, ok := str("compatibility"); present {
		s = strings.TrimSpace(s)
		switch {
		case !ok:
			errorf(RuleCompatibility, "the front matter key compatibility must be a string")
		case s == "" || utf8.RuneCountInString(s) > MaxCompatibilityLength:
			errorf(RuleCompatibility, "the compatibility must have 1 to %d characters", MaxCompatibilityLength)
		default:
			meta.Compatibility = s
		}
	}

	// allowed-tools
	if s, present, ok := str("allowed-tools"); present {
		switch {
		case !ok:
			errorf(RuleAllowedTools, "the front matter key allowed-tools must be a string of tool names separated by spaces")
		case strings.TrimSpace(s) == "":
			errorf(RuleAllowedTools, "allowed-tools must not be empty")
		default:
			meta.AllowedTools = strings.Join(strings.Fields(s), " ")
		}
	}

	// metadata
	if v, present := raw["metadata"]; present {
		m, ok := v.(map[string]any)
		if !ok {
			errorf(RuleMetadata, "the front matter key metadata must be a map of strings to strings")
		} else {
			meta.Metadata = make(map[string]string, len(m))
			mk := make([]string, 0, len(m))
			for k := range m {
				mk = append(mk, k)
			}
			sort.Strings(mk)
			for _, k := range mk {
				s, isString := m[k].(string)
				if !isString {
					errorf(RuleMetadata, "metadata.%s must be a string; quote the value", k)
					continue
				}
				meta.Metadata[k] = s
			}
		}
	}

	// body
	switch {
	case strings.TrimSpace(body) == "":
		errorf(RuleBodyEmpty, "the Markdown body is empty")
	case len(body) > MaxBodyBytes:
		errorf(RuleBodyTooLarge, "the Markdown body is %d bytes, above the limit of %d bytes", len(body), MaxBodyBytes)
	}
	if lines := BodyLines(body); lines > WarnBodyLines {
		warnf(RuleBodyLines, "the body has %d lines; the format recommends %d lines or fewer. Move detail to references/", lines, WarnBodyLines)
	}
	if tokens := EstimateTokens(len(body)); tokens > WarnBodyTokens {
		warnf(RuleBodyTokens, "the body is about %d tokens; the format recommends %d tokens or fewer. Move detail to references/", tokens, WarnBodyTokens)
	}
	return meta, body, issues
}

func isAllowedKey(k string) bool {
	return slices.Contains(allowedKeys, k)
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

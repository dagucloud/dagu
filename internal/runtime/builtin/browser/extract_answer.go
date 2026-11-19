// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"cmp"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// maxListedProblems bounds the problems an answer mismatch names.
const maxListedProblems = 5

// answerRoot is the first path element of a validation issue about the
// model's answer: the runtime checks the answer as the structuredContent of
// the model's response, which is no part of the step's schema.
const answerRoot = "structuredContent"

// answerMismatch is an extract answer that does not fit the step's schema,
// told in words.
type answerMismatch struct{ message string }

func (e answerMismatch) Error() string { return e.message }

// tidy drops, in place, the list items in value that are objects with only
// blank fields, as a model writes "nothing here", and returns value. Other items keep their place, since the
// position of a plain value in a list can carry meaning.
func tidy(value any) any {
	switch v := value.(type) {
	case map[string]any:
		for name, field := range v {
			v[name] = tidy(field)
		}
		return v
	case []any:
		kept := make([]any, 0, len(v))
		for _, item := range v {
			item = tidy(item)
			if _, row := item.(map[string]any); row && blank(item) {
				continue
			}
			kept = append(kept, item)
		}
		return kept
	}
	return value
}

// blank reports whether value holds nothing: null, an empty or blank
// string, or a list or object holding only blank values.
func blank(value any) bool {
	switch v := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(v) == ""
	case []any:
		for _, item := range v {
			if !blank(item) {
				return false
			}
		}
		return true
	case map[string]any:
		for _, field := range v {
			if !blank(field) {
				return false
			}
		}
		return true
	}
	return false
}

func article(noun string) string {
	if noun != "" && strings.ContainsRune("aeiou", rune(noun[0])) {
		return "an " + noun
	}
	return "a " + noun
}

// zodIssue is one entry of the validation report the runtime gives when a
// model's answer does not fit the schema.
type zodIssue struct {
	Code     string            `json:"code"`
	Expected string            `json:"expected"`
	Path     []json.RawMessage `json:"path"`
	Values   []any             `json:"values"`
	Keys     []string          `json:"keys"`
	Message  string            `json:"message"`
}

// describeValidationReport tells in words the runtime's validation report
// on the model's answer that message holds, or returns false when message
// is not one.
func describeValidationReport(message string) (answerMismatch, bool) {
	var issues []zodIssue
	if err := json.Unmarshal([]byte(strings.TrimSpace(message)), &issues); err != nil || len(issues) == 0 {
		return answerMismatch{}, false
	}
	problems := make([]string, 0, len(issues))
	for _, issue := range issues {
		if issue.Code == "" || !issue.aboutAnswer() {
			return answerMismatch{}, false
		}
		problems = append(problems, issue.describe())
	}
	listed := problems
	if len(listed) > maxListedProblems {
		listed = listed[:maxListedProblems]
	}
	text := "the model's answer does not fit the extract schema: " + strings.Join(listed, "; ")
	if more := len(problems) - len(listed); more > 0 {
		text += fmt.Sprintf("; and %d more", more)
	}
	return answerMismatch{message: text}, true
}

// aboutAnswer reports whether the issue concerns the model's answer rather
// than the request the runtime received.
func (i zodIssue) aboutAnswer() bool {
	if len(i.Path) == 0 {
		return false
	}
	var root string
	return json.Unmarshal(i.Path[0], &root) == nil && root == answerRoot
}

func (i zodIssue) describe() string {
	path := ""
	for _, raw := range i.Path[1:] {
		var index int
		if json.Unmarshal(raw, &index) == nil {
			path += "[" + strconv.Itoa(index) + "]"
			continue
		}
		var name string
		_ = json.Unmarshal(raw, &name)
		path = joinPath(path, name)
	}
	field := cmp.Or(path, "the answer")
	switch i.Code {
	case "invalid_type":
		return fmt.Sprintf("%s is not %s", field, article(i.Expected))
	case "invalid_value":
		values := make([]string, 0, len(i.Values))
		for _, value := range i.Values {
			encoded, _ := json.Marshal(value)
			values = append(values, string(encoded))
		}
		return fmt.Sprintf("%s is not one of %s", field, strings.Join(values, ", "))
	case "unrecognized_keys":
		return fmt.Sprintf("%s has fields the schema does not list: %s", field, strings.Join(i.Keys, ", "))
	}
	return fmt.Sprintf("%s: %s", field, i.Message)
}

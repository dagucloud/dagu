// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// maxListedFields bounds the fields an answer mismatch names.
const maxListedFields = 5

// answerMismatch is an extract answer that does not fit the step's schema,
// told in words.
type answerMismatch struct{ message string }

func (e answerMismatch) Error() string { return e.message }

// nullableSchema returns a copy of an extract schema in which every typed
// value below the top-level object may also be null. A model that finds
// nothing for a field writes null, which the runtime would otherwise reject
// with its own validation report; settleAnswer decides what such a null
// means against the original schema.
func nullableSchema(schema map[string]any) (json.RawMessage, error) {
	data, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}
	var relaxed map[string]any
	if err := json.Unmarshal(data, &relaxed); err != nil {
		return nil, err
	}
	relaxChildren(relaxed)
	return json.Marshal(relaxed)
}

func relaxChildren(schema map[string]any) {
	if properties, ok := schema["properties"].(map[string]any); ok {
		for _, property := range properties {
			if definition, ok := property.(map[string]any); ok {
				relax(definition)
			}
		}
	}
	if items, ok := schema["items"].(map[string]any); ok {
		relax(items)
	}
}

func relax(schema map[string]any) {
	switch t := schema["type"].(type) {
	case string:
		if t != "null" {
			schema["type"] = []any{t, "null"}
		}
	case []any:
		if !slices.Contains(t, any("null")) {
			schema["type"] = append(t, "null")
		}
	}
	if values, ok := schema["enum"].([]any); ok && !slices.Contains(values, nil) {
		schema["enum"] = append(values, nil)
	}
	relaxChildren(schema)
}

// settleAnswer turns values, an answer read with nullableSchema(schema),
// into one schema accepts. It drops the array items whose values are all
// null or empty, as a model writes "nothing here", turns a list answered
// null into an empty list, and leaves out optional properties answered
// null. It reports whether the answer holds anything; when it does, a null
// left where schema requires a value is an answerMismatch.
func settleAnswer(schema, values map[string]any) (bool, error) {
	tidy(schema, values)
	if blank(values) {
		return false, nil
	}
	var nulls []string
	findNulls(schema, values, "", &nulls)
	if len(nulls) == 0 {
		return true, nil
	}
	listed := nulls
	if len(listed) > maxListedFields {
		listed = listed[:maxListedFields]
	}
	message := "the model answered null for " + strings.Join(listed, ", ")
	if more := len(nulls) - len(listed); more > 0 {
		message += fmt.Sprintf(" and %d more", more)
	}
	return true, answerMismatch{message: message + ", which the extract schema requires"}
}

// tidy settles value in place as settleAnswer describes and returns it.
func tidy(schema map[string]any, value any) any {
	switch v := value.(type) {
	case map[string]any:
		properties, _ := schema["properties"].(map[string]any)
		required := requiredProperties(schema)
		for name, field := range v {
			definition, _ := properties[name].(map[string]any)
			field = tidy(definition, field)
			switch {
			case field == nil && typeOf(definition) == "array":
				v[name] = []any{}
			case field == nil && !required[name] && !allowsNull(definition):
				delete(v, name)
			default:
				v[name] = field
			}
		}
		return v
	case []any:
		items, _ := schema["items"].(map[string]any)
		kept := make([]any, 0, len(v))
		for _, item := range v {
			if item = tidy(items, item); !blank(item) {
				kept = append(kept, item)
			}
		}
		return kept
	}
	return value
}

// blank reports whether value holds nothing: null, an empty or blank
// string, or a list or object of blank values.
func blank(value any) bool {
	switch v := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(v) == ""
	case []any:
		return len(v) == 0
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

// findNulls adds to nulls each value under path that is null where schema
// does not allow null, naming the field and the type it expects.
func findNulls(schema map[string]any, value any, path string, nulls *[]string) {
	switch v := value.(type) {
	case nil:
		if !allowsNull(schema) {
			*nulls = append(*nulls, fmt.Sprintf("%s (expected %s)", cmpPath(path), describeType(schema)))
		}
	case map[string]any:
		properties, _ := schema["properties"].(map[string]any)
		for _, name := range slices.Sorted(maps.Keys(v)) {
			definition, _ := properties[name].(map[string]any)
			findNulls(definition, v[name], joinPath(path, name), nulls)
		}
	case []any:
		items, _ := schema["items"].(map[string]any)
		for i, item := range v {
			findNulls(items, item, path+"["+strconv.Itoa(i)+"]", nulls)
		}
	}
}

// allowsNull reports whether schema accepts null. A schema without a type
// is not relaxed by nullableSchema, so the runtime has already checked it.
func allowsNull(schema map[string]any) bool {
	switch t := schema["type"].(type) {
	case string:
		return t == "null"
	case []any:
		return slices.Contains(t, any("null"))
	}
	return true
}

func typeOf(schema map[string]any) string {
	t, _ := schema["type"].(string)
	return t
}

func describeType(schema map[string]any) string {
	switch t := schema["type"].(type) {
	case string:
		return article(t)
	case []any:
		names := make([]string, 0, len(t))
		for _, name := range t {
			names = append(names, fmt.Sprint(name))
		}
		return article(strings.Join(names, " or "))
	}
	return "a value"
}

func article(noun string) string {
	if noun != "" && strings.ContainsRune("aeiou", rune(noun[0])) {
		return "an " + noun
	}
	return "a " + noun
}

func requiredProperties(schema map[string]any) map[string]bool {
	names, _ := schema["required"].([]any)
	required := make(map[string]bool, len(names))
	for _, name := range names {
		if s, ok := name.(string); ok {
			required[s] = true
		}
	}
	return required
}

func cmpPath(path string) string {
	if path == "" {
		return "the answer"
	}
	return path
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
// that message holds, or returns false when message is not one.
func describeValidationReport(message string) (answerMismatch, bool) {
	var issues []zodIssue
	if err := json.Unmarshal([]byte(strings.TrimSpace(message)), &issues); err != nil || len(issues) == 0 {
		return answerMismatch{}, false
	}
	problems := make([]string, 0, len(issues))
	for _, issue := range issues {
		if issue.Code == "" {
			return answerMismatch{}, false
		}
		problems = append(problems, issue.describe())
	}
	listed := problems
	if len(listed) > maxListedFields {
		listed = listed[:maxListedFields]
	}
	text := "the model's answer does not fit the extract schema: " + strings.Join(listed, "; ")
	if more := len(problems) - len(listed); more > 0 {
		text += fmt.Sprintf("; and %d more", more)
	}
	return answerMismatch{message: text}, true
}

func (i zodIssue) describe() string {
	path := ""
	for n, raw := range i.Path {
		var index int
		if json.Unmarshal(raw, &index) == nil {
			path += "[" + strconv.Itoa(index) + "]"
			continue
		}
		var name string
		_ = json.Unmarshal(raw, &name)
		// The runtime checks the answer as the structuredContent of the
		// model's response, which is no part of the step's schema.
		if n == 0 && name == "structuredContent" {
			continue
		}
		path = joinPath(path, name)
	}
	field := cmpPath(path)
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

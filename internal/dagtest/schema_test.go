// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagtest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"

	cmnschema "github.com/dagucloud/dagu/v2/internal/cmn/schema"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestSchemaAccepts checks Spec 101's rule that the schema rejects a file for
// a field or a type only when the file does not decode. Spec examples hold
// only allowed values, so the full schema accepts exactly the ones that
// decode. Testdata also holds values that make only a case invalid, which
// Spec 101 lets the schema reject, so those files are checked for fields and
// types alone. A testdata file that does not decode is not checked: the
// decoder also enforces rules a schema cannot state, such as unique case
// names.
func TestSchemaAccepts(t *testing.T) {
	t.Parallel()

	full, fieldsAndTypes := resolveSchemas(t)
	for name, data := range specExamples(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, errs := decodeFile(name, data)
			err := validateYAML(full, data)
			require.Equal(t, len(errs) == 0, err == nil, "decoder: %v\nschema: %v", errs, err)
		})
	}
	for name, data := range testdataFiles(t) {
		if _, errs := decodeFile(name, data); len(errs) > 0 {
			continue
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.NoError(t, validateYAML(fieldsAndTypes, data))
		})
	}
}

// TestSchemaRejects checks that a file the decoder rejects for a field or a
// type fails the schema at every level. A value outside the allowed ones
// makes only its case invalid: the file decodes, and only the full schema
// rejects it.
func TestSchemaRejects(t *testing.T) {
	t.Parallel()

	const valid = "{name: c, expect: {status: succeeded}}"
	tests := []struct {
		name      string
		yaml      string
		caseError bool
	}{
		{name: "UnknownFileField", yaml: "{schedule: x, cases: [" + valid + "]}"},
		{name: "UnknownCaseField", yaml: "{cases: [{name: c, timeout: 5s, expect: {status: succeeded}}]}"},
		{name: "UnknownMockField", yaml: "{mocks: {build: {retries: 1}}, cases: [" + valid + "]}"},
		{name: "StepFieldInResult", yaml: "{mocks: {build: {results: [{precondition: met}]}}, cases: [" + valid + "]}"},
		{name: "UnknownReviewField", yaml: "{mocks: {gate: {review: {decision: approve, comment: ok}}}, cases: [" + valid + "]}"},
		{name: "UnknownExpectField", yaml: "{cases: [{name: c, expect: {status: succeeded, duration: 1s}}]}"},
		{name: "UnknownStepAssertion", yaml: "{cases: [{name: c, expect: {steps: {build: {exit_code: 0}}}}]}"},
		{name: "UnknownStreamAssertion", yaml: "{cases: [{name: c, expect: {steps: {build: {stdout: {equals: ok}}}}}]}"},
		{name: "UnknownCallAssertion", yaml: "{cases: [{name: c, expect: {steps: {build: {called_with: {args: [a]}}}}}]}"},
		{name: "CallRunList", yaml: "{cases: [{name: c, expect: {steps: {build: {called_with: {run: [a]}}}}}]}"},
		{name: "CallEnvNotScalar", yaml: "{cases: [{name: c, expect: {steps: {build: {called_with: {env: {A: [b]}}}}}}]}"},
		{name: "UnknownNotificationField", yaml: "{cases: [{name: c, expect: {notifications: [{kind: mail_on.failure, subject: x}]}}]}"},
		{name: "ReadsAsDAG", yaml: "{steps: [{run: ./smoke.sh}]}"},
		{name: "NoCases", yaml: "{mocks: {build: {}}}"},
		{name: "EmptyCases", yaml: "{cases: []}"},
		{name: "EmptyCaseName", yaml: `{cases: [{name: "", expect: {status: succeeded}}]}`},
		{name: "NoExpect", yaml: "{cases: [{name: c}]}"},
		{name: "NoDecision", yaml: "{mocks: {gate: {review: {values: {a: b}}}}, cases: [" + valid + "]}"},
		{name: "TopLevelList", yaml: "[" + valid + "]"},
		{name: "DAGNotString", yaml: "{dag: 1, cases: [" + valid + "]}"},
		{name: "ParamNotScalar", yaml: "{params: {list: [a]}, cases: [" + valid + "]}"},
		{name: "EnvNotMap", yaml: "{env: [A=1], cases: [" + valid + "]}"},
		{name: "RealNotList", yaml: "{real: build, cases: [" + valid + "]}"},
		{name: "CaseNotMap", yaml: "{cases: [a case]}"},
		{name: "ExitCodeNotInteger", yaml: `{mocks: {build: {exit_code: "0"}}, cases: [` + valid + "]}"},
		{name: "StdoutNotString", yaml: "{mocks: {build: {stdout: 1}}, cases: [" + valid + "]}"},
		{name: "ResultsNotList", yaml: "{mocks: {build: {results: {}}}, cases: [" + valid + "]}"},
		{name: "OutputsNotMap", yaml: "{mocks: {build: {outputs: [a]}}, cases: [" + valid + "]}"},
		{name: "ReviewNotMap", yaml: "{mocks: {gate: {review: approve}}, cases: [" + valid + "]}"},
		{name: "TriggerNotString", yaml: "{cases: [{name: c, trigger: 1, expect: {status: succeeded}}]}"},
		{name: "AttemptsNotInteger", yaml: "{cases: [{name: c, expect: {attempts: one}}]}"},
		{name: "StepAssertionList", yaml: "{cases: [{name: c, expect: {steps: {build: [succeeded]}}}]}"},
		{name: "RecipientsNotList", yaml: "{cases: [{name: c, expect: {notifications: [{kind: mail_on_error, to: a@example.com}]}}]}"},
		{name: "UnknownTrigger", yaml: "{cases: [{name: c, trigger: subdag, expect: {status: succeeded}}]}", caseError: true},
		{name: "UnknownPrecondition", yaml: "{cases: [{name: c, precondition: maybe, expect: {status: succeeded}}]}", caseError: true},
		{name: "UnknownCondition", yaml: "{mocks: {poll: {results: [{condition: sometimes}]}}, cases: [" + valid + "]}", caseError: true},
		{name: "UnknownDecision", yaml: "{mocks: {gate: {review: {decision: later}}}, cases: [" + valid + "]}", caseError: true},
		{name: "UnknownRunStatus", yaml: "{cases: [{name: c, expect: {status: done}}]}", caseError: true},
		{name: "UnknownStepStatus", yaml: "{cases: [{name: c, expect: {steps: {build: {status: skippedd}}}}]}", caseError: true},
		{name: "NegativeAttempts", yaml: "{cases: [{name: c, expect: {steps: {build: {attempts: -1}}}}]}", caseError: true},
		{name: "UnknownNotificationKind", yaml: "{cases: [{name: c, expect: {notifications: [{kind: mail}]}}]}", caseError: true},
	}
	full, fieldsAndTypes := resolveSchemas(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			data := []byte(tt.yaml)
			_, errs := decodeFile(tt.name, data)
			require.Error(t, validateYAML(full, data))
			if tt.caseError {
				require.Empty(t, errs)
				require.NoError(t, validateYAML(fieldsAndTypes, data))
			} else {
				require.NotEmpty(t, errs)
				require.Error(t, validateYAML(fieldsAndTypes, data))
			}
		})
	}
}

// TestSchemaFields checks that the schema and the decoder take the same
// fields, so neither gains one alone. Each spec struct below holds one Go
// field per YAML field, its name in camel case.
func TestSchemaFields(t *testing.T) {
	t.Parallel()

	root := parseSchema(t)
	tests := []struct {
		spec   any
		schema *jsonschema.Schema
	}{
		{fileSpec{}, root},
		{caseSpec{}, root.Definitions["case"]},
		{resultSpec{}, root.Definitions["result"]},
		{reviewSpec{}, root.Definitions["review"]},
		{streamSpec{}, root.Definitions["stream"]},
		{callSpec{}, root.Definitions["calledWith"]},
		{notificationSpec{}, root.Definitions["notification"]},
	}
	for _, tt := range tests {
		var fields []string
		for field := range reflect.TypeOf(tt.spec).Fields() {
			fields = append(fields, snakeCase(field.Name))
		}
		assert.ElementsMatch(t, fields, slices.Collect(maps.Keys(tt.schema.Properties)), "%T", tt.spec)
	}
}

// TestSchemaValues checks that the schema allows exactly the status tokens
// and notification kinds the decoder accepts, and only triggers and met
// values the decoder accepts.
func TestSchemaValues(t *testing.T) {
	t.Parallel()

	root := parseSchema(t)
	assert.Equal(t, runStatuses, enumOf(root.Definitions["expect"].Properties["status"]))
	assert.Equal(t, nodeStatuses, enumOf(root.Definitions["nodeStatus"]))
	assert.Equal(t, notificationKinds, enumOf(root.Definitions["notification"].Properties["kind"]))

	b := &caseBuilder{}
	for _, trigger := range enumOf(root.Definitions["case"].Properties["trigger"]) {
		b.trigger(&trigger)
	}
	for _, met := range enumOf(root.Definitions["met"]) {
		b.met("met", &met)
	}
	assert.Empty(t, b.errs)
}

func parseSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()

	var schema jsonschema.Schema
	require.NoError(t, json.Unmarshal(cmnschema.WorkflowTestSchemaJSON, &schema))
	return &schema
}

// resolveSchemas returns the full schema, and the schema of fields and
// types alone: without enum and minimum, the keywords that state allowed
// values. Spec 101 makes a value outside them a case error, while an
// unknown field or a wrong type makes the file invalid.
func resolveSchemas(t *testing.T) (full, fieldsAndTypes *jsonschema.Resolved) {
	t.Helper()

	resolve := func(schema *jsonschema.Schema) *jsonschema.Resolved {
		resolved, err := schema.Resolve(&jsonschema.ResolveOptions{})
		require.NoError(t, err)
		return resolved
	}
	reduced := parseSchema(t)
	dropAllowedValues(reduced)
	return resolve(parseSchema(t)), resolve(reduced)
}

func dropAllowedValues(s *jsonschema.Schema) {
	if s == nil {
		return
	}
	s.Enum, s.Minimum = nil, nil
	for _, child := range s.Properties {
		dropAllowedValues(child)
	}
	for _, child := range s.Definitions {
		dropAllowedValues(child)
	}
	for _, child := range s.AnyOf {
		dropAllowedValues(child)
	}
	dropAllowedValues(s.Items)
	dropAllowedValues(s.AdditionalProperties)
}

// validateYAML validates each YAML document of data against schema, as an
// editor does. YAML that does not parse fails.
func validateYAML(schema *jsonschema.Resolved, data []byte) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var doc any
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := schema.Validate(datesAsText(doc)); err != nil {
			return err
		}
	}
}

// datesAsText replaces each time that a YAML timestamp decodes to with a
// string, since editors and the decoder read an unquoted date as text.
func datesAsText(v any) any {
	switch v := v.(type) {
	case map[string]any:
		for key, item := range v {
			v[key] = datesAsText(item)
		}
	case []any:
		for i, item := range v {
			v[i] = datesAsText(item)
		}
	case time.Time:
		return v.Format(time.RFC3339Nano)
	}
	return v
}

var (
	yamlBlock = regexp.MustCompile("(?ms)^```yaml\n(.*?)^```")
	// testFileLine matches a line that only a test file holds: a label that
	// names a test file, or top-level cases.
	testFileLine = regexp.MustCompile(`(?m)^(?:# \S+\.test\.ya?ml$|cases:)`)
)

// specExamples returns the YAML blocks of Spec 101 that hold a test file, by
// the line each starts on. A test file line outside a yaml block fails the
// test, so a change to the spec's format cannot leave an example unchecked.
func specExamples(t *testing.T) map[string][]byte {
	t.Helper()

	path := filepath.Join("..", "..", "specs", "101-workflow-tests.md")
	data, err := os.ReadFile(path)
	require.NoError(t, err, "read Spec 101")
	// A checkout ends the spec's lines with CRLF on Windows (.gitattributes
	// text=auto); the blocks and their lines are matched by LF.
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	lineOf := func(offset int) int { return bytes.Count(data[:offset], []byte("\n")) + 1 }

	blocks := yamlBlock.FindAllSubmatchIndex(data, -1)
	examples := make(map[string][]byte)
	for _, line := range testFileLine.FindAllIndex(data, -1) {
		i := slices.IndexFunc(blocks, func(b []int) bool { return b[2] <= line[0] && line[0] < b[3] })
		require.GreaterOrEqual(t, i, 0, "%s:%d holds a test file outside a ```yaml block; update specExamples to read it", path, lineOf(line[0]))
		start, end := blocks[i][2], blocks[i][3]
		examples[fmt.Sprintf("Spec101Line%d", lineOf(start))] = data[start:end]
	}
	require.NotEmpty(t, examples, "%s holds no test file example; update specExamples to read them", path)
	return examples
}

// testdataFiles returns the test files under testdata/testfiles by their
// path in it.
func testdataFiles(t *testing.T) map[string][]byte {
	t.Helper()

	dir := filepath.Join("testdata", "testfiles")
	files := make(map[string][]byte)
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !spec.IsTestFile(path) {
			return err
		}
		name, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(name)], err = os.ReadFile(path)
		return err
	})
	require.NoError(t, err)
	require.NotEmpty(t, files, "%s holds no test file", dir)
	return files
}

func enumOf(schema *jsonschema.Schema) []string {
	var values []string
	for _, v := range schema.Enum {
		values = append(values, v.(string))
	}
	return values
}

func snakeCase(name string) string {
	var b strings.Builder
	for _, r := range name {
		if unicode.IsUpper(r) {
			b.WriteByte('_')
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}

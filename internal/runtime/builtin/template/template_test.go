// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package template

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateTemplateRequiresScriptMessage(t *testing.T) {
	err := validateTemplate(core.Step{})
	require.Error(t, err)
	assert.Equal(t, "field 'script': script field is required", err.Error())
	assert.NotContains(t, err.Error(), "executor")
}

func TestValidateTemplateAcceptsScopedTemplateReference(t *testing.T) {
	t.Parallel()

	err := validateTemplate(core.Step{
		ExecutorConfig: core.ExecutorConfig{
			Config: map[string]any{"template_ref": "${env.TEMPLATE}"},
		},
	})
	require.NoError(t, err)
}

func TestValidateTemplateRejectsAmbiguousTemplateReference(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		step core.Step
		err  string
	}{
		{
			name: "script and reference",
			step: core.Step{
				Script: "literal",
				ExecutorConfig: core.ExecutorConfig{
					Config: map[string]any{"template_ref": "${env.TEMPLATE}"},
				},
			},
			err: "cannot use both script and with.template_ref",
		},
		{
			name: "invalid reference",
			step: core.Step{
				ExecutorConfig: core.ExecutorConfig{
					Config: map[string]any{"template_ref": "TEMPLATE"},
				},
			},
			err: "must be one complete scoped value reference",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateTemplate(tt.step)
			require.ErrorContains(t, err, tt.err)
		})
	}
}

func TestNewTemplateRequiresScriptMessage(t *testing.T) {
	_, err := newTemplate(context.Background(), core.Step{})
	require.Error(t, err)
	assert.Equal(t, "field 'script': script field is required", err.Error())
	assert.NotContains(t, err.Error(), "executor")
}

func TestTemplateExec_BasicStdout(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	e := &templateExec{
		stdout: &stdout,
		stderr: os.Stderr,
		script: "Hello, World!",
		data:   map[string]any{},
	}

	err := e.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "Hello, World!", stdout.String())
}

func TestTemplateExec_DataVariables(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	e := &templateExec{
		stdout: &stdout,
		stderr: os.Stderr,
		script: "Hello, {{ .name }}! You have {{ .count }} items.",
		data: map[string]any{
			"name":  "Alice",
			"count": 42,
		},
	}

	err := e.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "Hello, Alice! You have 42 items.", stdout.String())
}

func TestTemplateExec_OutputToFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	outFile := filepath.Join(tmpDir, "output.txt")

	var stdout bytes.Buffer
	e := &templateExec{
		stdout:     &stdout,
		stderr:     os.Stderr,
		script:     "File content: {{ .msg }}",
		data:       map[string]any{"msg": "hello"},
		outputFile: outFile,
	}

	err := e.Run(context.Background())
	require.NoError(t, err)

	// stdout should be empty when writing to file
	assert.Empty(t, stdout.String())

	// file should contain the rendered content
	content, err := os.ReadFile(outFile)
	require.NoError(t, err)
	assert.Equal(t, "File content: hello", string(content))
}

func TestTemplateExec_OutputToFileCreatesSubdirs(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	outFile := filepath.Join(tmpDir, "sub", "dir", "output.txt")

	e := &templateExec{
		stdout:     &bytes.Buffer{},
		stderr:     os.Stderr,
		script:     "nested",
		data:       map[string]any{},
		outputFile: outFile,
	}

	err := e.Run(context.Background())
	require.NoError(t, err)

	content, err := os.ReadFile(outFile)
	require.NoError(t, err)
	assert.Equal(t, "nested", string(content))
}

func TestTemplateExec_MissingKeyError(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	e := &templateExec{
		stdout: &stdout,
		stderr: os.Stderr,
		script: "{{ .undefined_key }}",
		data:   map[string]any{},
	}

	err := e.Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "execution error")
}

func TestTemplateExec_InvalidSyntax(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	e := &templateExec{
		stdout: &stdout,
		stderr: os.Stderr,
		script: "{{ .foo",
		data:   map[string]any{},
	}

	err := e.Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse error")
}

func TestTemplateExec_EmptyDataLiterals(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	e := &templateExec{
		stdout: &stdout,
		stderr: os.Stderr,
		script: "Just literal text, no variables.",
		data:   map[string]any{},
	}

	err := e.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "Just literal text, no variables.", stdout.String())
}

func TestFuncMap_Split(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	e := &templateExec{
		stdout: &stdout,
		stderr: os.Stderr,
		script: `{{ range .items | split "," }}[{{ . }}]{{ end }}`,
		data:   map[string]any{"items": "a,b,c"},
	}

	err := e.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "[a][b][c]", stdout.String())
}

func TestFuncMap_Join(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	e := &templateExec{
		stdout: &stdout,
		stderr: os.Stderr,
		script: `{{ .items | join ", " }}`,
		data:   map[string]any{"items": []string{"a", "b", "c"}},
	}

	err := e.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "a, b, c", stdout.String())
}

func TestFuncMap_Count(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	e := &templateExec{
		stdout: &stdout,
		stderr: os.Stderr,
		script: `{{ .items | count }}`,
		data:   map[string]any{"items": []string{"a", "b", "c"}},
	}

	err := e.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "3", stdout.String())
}

func TestFuncMap_Add(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	e := &templateExec{
		stdout: &stdout,
		stderr: os.Stderr,
		script: `{{ 5 | add 3 }}`,
		data:   map[string]any{},
	}

	err := e.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "8", stdout.String())
}

func TestFuncMap_Empty(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	e := &templateExec{
		stdout: &stdout,
		stderr: os.Stderr,
		script: `empty={{ .val | empty }},notempty={{ .other | empty }}`,
		data:   map[string]any{"val": "", "other": "hello"},
	}

	err := e.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "empty=true,notempty=false", stdout.String())
}

func TestFuncMap_Upper(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	e := &templateExec{
		stdout: &stdout,
		stderr: os.Stderr,
		script: `{{ .val | upper }}`,
		data:   map[string]any{"val": "hello"},
	}

	err := e.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "HELLO", stdout.String())
}

func TestFuncMap_Lower(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	e := &templateExec{
		stdout: &stdout,
		stderr: os.Stderr,
		script: `{{ .val | lower }}`,
		data:   map[string]any{"val": "HELLO"},
	}

	err := e.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "hello", stdout.String())
}

func TestFuncMap_Trim(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	e := &templateExec{
		stdout: &stdout,
		stderr: os.Stderr,
		script: `[{{ .val | trim }}]`,
		data:   map[string]any{"val": "  hello  "},
	}

	err := e.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "[hello]", stdout.String())
}

func TestFuncMap_Default_WithEmptyValue(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	e := &templateExec{
		stdout: &stdout,
		stderr: os.Stderr,
		script: `{{ .val | default "N/A" }}`,
		data:   map[string]any{"val": ""},
	}

	err := e.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "N/A", stdout.String())
}

func TestFuncMap_Default_WithNonEmptyValue(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	e := &templateExec{
		stdout: &stdout,
		stderr: os.Stderr,
		script: `{{ .val | default "N/A" }}`,
		data:   map[string]any{"val": "present"},
	}

	err := e.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "present", stdout.String())
}

func TestFuncMap_PipelineChaining(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	e := &templateExec{
		stdout: &stdout,
		stderr: os.Stderr,
		script: `{{ .val | split "\n" | count }}`,
		data:   map[string]any{"val": "a\nb\nc"},
	}

	err := e.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "3", stdout.String())
}

func TestFuncMap_EmptySlice(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	e := &templateExec{
		stdout: &stdout,
		stderr: os.Stderr,
		script: `{{ .items | empty }}`,
		data:   map[string]any{"items": []string{}},
	}

	err := e.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "true", stdout.String())
}

func TestFuncMap_EmptyNil(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	e := &templateExec{
		stdout: &stdout,
		stderr: os.Stderr,
		script: `{{ .val | empty }}`,
		data:   map[string]any{"val": nil},
	}

	err := e.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "true", stdout.String())
}

func TestSlimSprig_Get(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	e := &templateExec{
		stdout: &stdout,
		stderr: os.Stderr,
		script: `{{ get .app "owner" }}`,
		data: map[string]any{
			"app": map[string]any{"name": "test"},
		},
	}

	err := e.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "", stdout.String())
}

func TestSlimSprig_Dig(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	e := &templateExec{
		stdout: &stdout,
		stderr: os.Stderr,
		script: `{{ dig "a" "b" "fallback" .data }}`,
		data: map[string]any{
			"data": map[string]any{
				"a": map[string]any{"b": "found"},
			},
		},
	}

	err := e.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "found", stdout.String())
}

func TestSlimSprig_Replace(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	e := &templateExec{
		stdout: &stdout,
		stderr: os.Stderr,
		script: `{{ "hello world" | replace "world" "dagu" }}`,
		data:   map[string]any{},
	}

	err := e.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "hello dagu", stdout.String())
}

func TestSlimSprig_ListUniqSortAlpha(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	e := &templateExec{
		stdout: &stdout,
		stderr: os.Stderr,
		script: `{{ list "c" "a" "b" "a" | uniq | sortAlpha | join "," }}`,
		data:   map[string]any{},
	}

	err := e.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "a,b,c", stdout.String())
}

func TestSlimSprig_Has(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	e := &templateExec{
		stdout: &stdout,
		stderr: os.Stderr,
		script: `{{ list "a" "b" | has "a" }}`,
		data:   map[string]any{},
	}

	err := e.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "true", stdout.String())
}

func TestSlimSprig_Contains(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	e := &templateExec{
		stdout: &stdout,
		stderr: os.Stderr,
		script: `{{ contains "ell" "hello" }}`,
		data:   map[string]any{},
	}

	err := e.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "true", stdout.String())
}

func TestSlimSprig_HasPrefixSuffix(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	e := &templateExec{
		stdout: &stdout,
		stderr: os.Stderr,
		script: `{{ hasPrefix "hel" "hello" }},{{ hasSuffix "llo" "hello" }}`,
		data:   map[string]any{},
	}

	err := e.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "true,true", stdout.String())
}

func TestSlimSprig_Dict(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	e := &templateExec{
		stdout: &stdout,
		stderr: os.Stderr,
		script: `{{ $d := dict "key" "value" }}{{ get $d "key" }}`,
		data:   map[string]any{},
	}

	err := e.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "value", stdout.String())
}

func TestSlimSprig_ToString(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	e := &templateExec{
		stdout: &stdout,
		stderr: os.Stderr,
		script: `{{ 42 | toString }}`,
		data:   map[string]any{},
	}

	err := e.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "42", stdout.String())
}

func TestSlimSprig_BlockedFunctions(t *testing.T) {
	t.Parallel()

	// Verify blocked function names are not in the funcMap.
	for _, name := range blockedFuncs {
		_, exists := funcMap[name]
		assert.False(t, exists, "blocked function %q should not be in funcMap", name)
	}
}

func TestSlimSprig_BlockedFunctionErrors(t *testing.T) {
	t.Parallel()

	blockedTemplates := []string{
		`{{ env "HOME" }}`,
		`{{ expandenv "$HOME" }}`,
		`{{ now }}`,
		`{{ randAlphaNum 10 }}`,
	}

	for _, script := range blockedTemplates {
		var stdout bytes.Buffer
		e := &templateExec{
			stdout: &stdout,
			stderr: os.Stderr,
			script: script,
			data:   map[string]any{},
		}
		err := e.Run(context.Background())
		assert.Error(t, err, "script %q should fail", script)
	}
}

func TestSlimSprig_OverlapSplitPipeline(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	e := &templateExec{
		stdout: &stdout,
		stderr: os.Stderr,
		script: `{{ "a,b,c" | split "," | join ";" }}`,
		data:   map[string]any{},
	}

	err := e.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "a;b;c", stdout.String())
}

func TestSlimSprig_OverlapAddPipeline(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	e := &templateExec{
		stdout: &stdout,
		stderr: os.Stderr,
		script: `{{ 5 | add 3 }}`,
		data:   map[string]any{},
	}

	err := e.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "8", stdout.String())
}

func TestFuncMap_JoinGenericSlices(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		sep      string
		input    any
		expected string
	}{
		{
			name:     "typed int slice",
			sep:      ",",
			input:    []int{80, 443},
			expected: "80,443",
		},
		{
			name:     "string array",
			sep:      "-",
			input:    [2]string{"a", "b"},
			expected: "a-b",
		},
		{
			name:     "nil input",
			sep:      ",",
			input:    nil,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fn, ok := funcMap["join"].(func(string, any) (string, error))
			require.Truef(t, ok, "join has unexpected signature: %T", funcMap["join"])
			result, err := fn(tt.sep, tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFuncMap_SnapshotAllowedFunctions(t *testing.T) {
	t.Parallel()

	m := buildFuncMap()

	var got []string
	for name := range m {
		got = append(got, name)
	}
	sort.Strings(got)

	// This list is the complete set of functions exposed by buildFuncMap().
	// If a slim-sprig upgrade adds new functions, this test will fail,
	// forcing a deliberate review before they become available in templates.
	expected := []string{
		"add",
		"add1",
		"adler32sum",
		"all",
		"any",
		"append",
		"atoi",
		"b32dec",
		"b32enc",
		"b64dec",
		"b64enc",
		"base",
		"biggest",
		"cat",
		"ceil",
		"chunk",
		"clean",
		"coalesce",
		"compact",
		"concat",
		"contains",
		"count",
		"deepEqual",
		"default",
		"dict",
		"dig",
		"dir",
		"div",
		"empty",
		"ext",
		"fail",
		"first",
		"float64",
		"floor",
		"fromJson",
		"get",
		"has",
		"hasKey",
		"hasPrefix",
		"hasSuffix",
		"hello",
		"indent",
		"initial",
		"int",
		"int64",
		"isAbs",
		"join",
		"keys",
		"kindIs",
		"kindOf",
		"last",
		"list",
		"lower",
		"max",
		"maxf",
		"min",
		"minf",
		"mod",
		"mul",
		"mustAppend",
		"mustChunk",
		"mustCompact",
		"mustFirst",
		"mustFromJson",
		"mustHas",
		"mustInitial",
		"mustLast",
		"mustPrepend",
		"mustPush",
		"mustRegexFind",
		"mustRegexFindAll",
		"mustRegexMatch",
		"mustRegexReplaceAll",
		"mustRegexReplaceAllLiteral",
		"mustRegexSplit",
		"mustRest",
		"mustReverse",
		"mustSlice",
		"mustToJson",
		"mustToPrettyJson",
		"mustToRawJson",
		"mustUniq",
		"mustWithout",
		"nindent",
		"omit",
		"osBase",
		"osClean",
		"osDir",
		"osExt",
		"osIsAbs",
		"pick",
		"pluck",
		"plural",
		"prepend",
		"push",
		"quote",
		"regexFind",
		"regexFindAll",
		"regexMatch",
		"regexQuoteMeta",
		"regexReplaceAll",
		"regexReplaceAllLiteral",
		"regexSplit",
		"repeat",
		"replace",
		"rest",
		"reverse",
		"round",
		"seq",
		"set",
		"sha1sum",
		"sha256sum",
		"slice",
		"sortAlpha",
		"split",
		"splitList",
		"splitn",
		"squote",
		"sub",
		"substr",
		"ternary",
		"title",
		"toDecimal",
		"toJson",
		"toPrettyJson",
		"toRawJson",
		"toString",
		"toStrings",
		"trim",
		"trimAll",
		"trimPrefix",
		"trimSuffix",
		"trimall",
		"trunc",
		"tuple",
		"typeIs",
		"typeIsLike",
		"typeOf",
		"uniq",
		"unset",
		"until",
		"untilStep",
		"upper",
		"urlJoin",
		"urlParse",
		"values",
		"without",
	}

	assert.Equal(t, expected, got,
		"buildFuncMap() returned unexpected functions; review new entries before updating this list")
}

func TestSlimSprig_CrossLibraryPipeline(t *testing.T) {
	t.Parallel()

	// list (sprig) → join (Dagu override accepting []any)
	var stdout bytes.Buffer
	e := &templateExec{
		stdout: &stdout,
		stderr: os.Stderr,
		script: `{{ list "x" "y" "z" | join "-" }}`,
		data:   map[string]any{},
	}

	err := e.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "x-y-z", stdout.String())
}

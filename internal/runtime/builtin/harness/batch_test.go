// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The wrappers in testdata/shims were written by npm's cmd-shim 3, 8, and 9
// and pnpm's @zkochan/cmd-shim 9 for a package bin at
// node_modules/@scope/tool/bin/cli.js, or cli.cmd for the batch fixture.
// Generators write CRLF, and a checkout may convert it, so both line endings
// are parsed.
func TestParseNodeShim(t *testing.T) {
	t.Parallel()

	const script = `node_modules\@scope\tool\bin\cli.js`
	tests := []struct {
		file string
		want nodeShim
	}{
		{file: "npm-cmd-shim-3.cmd", want: nodeShim{prog: "node", script: script, stripJSPath: true}},
		{file: "npm-cmd-shim-8.cmd", want: nodeShim{prog: "node", script: script}},
		{file: "npm-cmd-shim-9.cmd", want: nodeShim{prog: "node", script: script, stripJSLocal: true, stripJSPath: true}},
		{
			file: "npm-cmd-shim-9-shebang-args.cmd",
			want: nodeShim{
				prog:         "node",
				args:         []string{"--max-old-space-size=4096"},
				script:       `node_modules\argtool\bin\cli.js`,
				stripJSLocal: true,
				stripJSPath:  true,
			},
		},
		{file: "pnpm-cmd-shim.cmd", want: nodeShim{prog: "node", script: script, stripJSPath: true}},
		{
			file: "pnpm-cmd-shim-batch.cmd",
			want: nodeShim{prog: "cmd", args: []string{"/C"}, script: `node_modules\@scope\tool\bin\cli.cmd`, stripJSPath: true},
		},
		{
			file: "pnpm-cmd-shim-node-path.cmd",
			want: nodeShim{
				prog:        "node",
				script:      script,
				nodePath:    `C:\pnpm\global\5\node_modules;C:\pnpm\global\5\.pnpm\node_modules`,
				stripJSPath: true,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			t.Parallel()
			content := readShimFixture(t, tt.file)
			for _, lineEnding := range []string{"\n", "\r\n"} {
				got, ok := parseNodeShim(strings.ReplaceAll(content, "\n", lineEnding))
				require.True(t, ok)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

// A wrapper is replaced by its program only when every line is one a
// generator writes, so no other effect of the wrapper is lost.
func TestParseNodeShimRejectsOtherWrappers(t *testing.T) {
	t.Parallel()

	npm := readShimFixture(t, "npm-cmd-shim-9.cmd")
	for name, content := range map[string]string{
		"PrependsPath":    readShimFixture(t, "pnpm-cmd-shim-prepend-path.cmd"),
		"HandWritten":     "@\"%~dp0\\tool.exe\" %*\n",
		"SetsVariable":    strings.Replace(npm, "CALL :find_dp0\n", "CALL :find_dp0\nSET \"FOO=bar\"\n", 1),
		"MixedPrograms":   strings.Replace(npm, `SET "_prog=node"`, `SET "_prog=python"`, 1),
		"ArgumentsBefore": strings.Replace(npm, `"%_prog%"  "%dp0%`, `"%_prog%" "%~1" "%dp0%`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, ok := parseNodeShim(content)
			assert.False(t, ok)
		})
	}
}

func TestBatchCommandLine(t *testing.T) {
	t.Parallel()

	const prefix = `cmd.exe /e:ON /v:OFF /d /c ""C:\npm\tool.cmd"`
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "PlainWords", args: []string{"-p", "C:/work/file.txt"}, want: ` -p C:/work/file.txt`},
		{name: "Metacharacters", args: []string{`" & calc & "`}, want: ` """ & calc & """`},
		{name: "Variables", args: []string{"%PATH%"}, want: ` "%%cd:~,%PATH%%cd:~,%"`},
		{name: "Empty", args: []string{""}, want: ` ""`},
		{name: "TrailingBackslash", args: []string{`C:\dir\`}, want: ` "C:\dir\\"`},
		{name: "BackslashBeforeQuote", args: []string{`a\"b`}, want: ` "a\\""b"`},
		{name: "NonASCII", args: []string{"naïve"}, want: ` naïve`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := batchCommandLine(`C:\npm\tool.cmd`, tt.args)
			require.NoError(t, err)
			assert.Equal(t, prefix+tt.want+`"`, got)
		})
	}
}

func TestBatchCommandLineRejectsUnpassableArguments(t *testing.T) {
	t.Parallel()

	_, err := batchCommandLine(`C:\npm\tool.cmd`, []string{"-p", "first line\nsecond line"})
	require.ErrorIs(t, err, errBatchLineBreak)

	_, err = batchCommandLine(`C:\npm\tool.cmd`, []string{strings.Repeat("a", maxBatchCommandLine)})
	require.ErrorContains(t, err, "cmd.exe accepts at most")

	// The limit counts UTF-16 code units, so a prompt longer in UTF-8 bytes
	// than the limit still fits.
	_, err = batchCommandLine(`C:\npm\tool.cmd`, []string{strings.Repeat("あ", 3000)})
	require.NoError(t, err)

	_, err = batchCommandLine(`C:\np"m\tool.cmd`, nil)
	require.ErrorContains(t, err, "double quote")
}

// A directory may be named like a variable; cmd.exe must open that path,
// not the one the variable holds.
func TestBatchCommandLineEscapesScriptPath(t *testing.T) {
	t.Parallel()

	got, err := batchCommandLine(`C:\%TEMP%\tool.cmd`, []string{"-p"})
	require.NoError(t, err)
	assert.Equal(t, `cmd.exe /e:ON /v:OFF /d /c ""C:\%%cd:~,%TEMP%%cd:~,%\tool.cmd" -p"`, got)
}

func TestNodeShimEnviron(t *testing.T) {
	t.Parallel()

	shim := nodeShim{nodePath: `C:\pnpm\node_modules`}

	env := shim.environ([]string{"Path=C:\\bin", "PATHEXT=.COM;.EXE;.CMD;.js;.JSE"}, true)
	assert.Equal(t, []string{"Path=C:\\bin", `NODE_PATH=C:\pnpm\node_modules`, "PATHEXT=.COM;.EXE;.CMD;.JSE"}, env)

	env = shim.environ([]string{`node_path=C:\old`, "PATHEXT=.COM;.EXE;.JS;.JSE"}, false)
	assert.Equal(t, []string{"PATHEXT=.COM;.EXE;.JS;.JSE", `NODE_PATH=C:\pnpm\node_modules;C:\old`}, env)
}

func readShimFixture(t *testing.T, name string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("testdata", "shims", name))
	require.NoError(t, err)
	return strings.ReplaceAll(string(content), "\r\n", "\n")
}

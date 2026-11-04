// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Each command marks the queried byte with @.
func TestShellContextAt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		command string
		want    shellContext
	}{
		{"unquoted", `echo @`, shellUnquoted},
		{"single", `echo 'a @'`, shellSingleQuoted},
		{"double", `echo "a @"`, shellDoubleQuoted},
		{"apostrophe in double quotes", `echo "it's" @`, shellUnquoted},
		{"escaped apostrophe", `echo \' @`, shellUnquoted},
		{"even backslashes", `echo \\'@'`, shellSingleQuoted},
		{"ANSI-C quotes", `echo $'it\'s @'`, shellSingleQuoted},
		{"substitution in double quotes", `echo "$(printf '"') @"`, shellDoubleQuoted},
		{"backquotes in double quotes", "echo \"`printf '\"'` @\"", shellDoubleQuoted},
		{"parameter expansion word", `echo "${X:-"it's"}" @`, shellUnquoted},
		{"parenthesis in a parameter expansion", `echo "$(echo "${X:-)}") @"`, shellDoubleQuoted},
		{"comment", "# don't\necho @", shellUnquoted},
		{"hash inside a word", `echo a#'@'`, shellSingleQuoted},
		{"here-document body", "cat <<EOF\nit's @\nEOF", shellHereDoc},
		{"after a here-document", "cat <<EOF\nit's\nEOF\necho @", shellUnquoted},
		{"tab-stripped delimiter", "cat <<-'END'\n\tx\n\tEND\necho '@'", shellSingleQuoted},
		{"here-string is not a here-document", "cat <<< 'x'\necho @", shellUnquoted},
		{"arithmetic shift", "echo $((1 << 2))\necho @", shellUnquoted},
		{"arithmetic command", "(( x << 2 ))\necho @", shellUnquoted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			idx := strings.IndexByte(tt.command, '@')
			assert.Equal(t, tt.want, shellContextAt(tt.command, idx))
		})
	}
}

// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package clickhouse

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSplitStatements(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		script string
		want   []string
	}{
		{"Single", "SELECT 1", []string{"SELECT 1"}},
		{"TrailingSemicolon", "SELECT 1;", []string{"SELECT 1"}},
		{"Several", "SELECT 1; SELECT 2;\nSELECT 3", []string{"SELECT 1", "SELECT 2", "SELECT 3"}},
		{"Empty", "", nil},
		{"OnlySemicolons", " ; ;\n;", nil},
		{"EmptyStatementsSkipped", "SELECT 1;;;SELECT 2", []string{"SELECT 1", "SELECT 2"}},
		{"StringWithSemicolon", "SELECT 'a;b'; SELECT 2", []string{"SELECT 'a;b'", "SELECT 2"}},
		{"DoubledQuote", "SELECT 'it''s; ok'; SELECT 2", []string{"SELECT 'it''s; ok'", "SELECT 2"}},
		{"BackslashQuote", `SELECT 'it\'s; ok'; SELECT 2`, []string{`SELECT 'it\'s; ok'`, "SELECT 2"}},
		{"BackslashBackslashEndsString", `SELECT 'a\\'; SELECT 2`, []string{`SELECT 'a\\'`, "SELECT 2"}},
		{"DoubleQuotedIdentifier", `SELECT "a;b" FROM t; SELECT 2`, []string{`SELECT "a;b" FROM t`, "SELECT 2"}},
		{"BacktickIdentifier", "SELECT `a;b` FROM t; SELECT 2", []string{"SELECT `a;b` FROM t", "SELECT 2"}},
		{"BacktickEscapedQuote", "SELECT `a\\`;b` FROM t; SELECT 2", []string{"SELECT `a\\`;b` FROM t", "SELECT 2"}},
		{"DoubledBacktick", "SELECT `a``;b` FROM t; SELECT 2", []string{"SELECT `a``;b` FROM t", "SELECT 2"}},
		{"QuoteInsideOtherQuote", `SELECT "it's"; SELECT '"'; SELECT 3`, []string{`SELECT "it's"`, `SELECT '"'`, "SELECT 3"}},
		{"LineComment", "SELECT 1 -- a; b\n; SELECT 2", []string{"SELECT 1 -- a; b", "SELECT 2"}},
		{"LineCommentWithQuote", "SELECT 1 -- it's\n; SELECT 2", []string{"SELECT 1 -- it's", "SELECT 2"}},
		{"HashComment", "SELECT 1 # a; b\n; SELECT 2", []string{"SELECT 1 # a; b", "SELECT 2"}},
		{"HashBangComment", "#!shebang; here\nSELECT 1", []string{"#!shebang; here\nSELECT 1"}},
		{"HashWithoutSpaceIsNotAComment", "SELECT 1 #; SELECT 2", []string{"SELECT 1 #", "SELECT 2"}},
		{"BlockComment", "SELECT 1 /* a; b */; SELECT 2", []string{"SELECT 1 /* a; b */", "SELECT 2"}},
		{"BlockCommentMultiline", "SELECT /* a;\n b; */ 1; SELECT 2", []string{"SELECT /* a;\n b; */ 1", "SELECT 2"}},
		{"BlockCommentWithQuote", "SELECT /* it's */ 1; SELECT 2", []string{"SELECT /* it's */ 1", "SELECT 2"}},
		{"UnterminatedBlockComment", "SELECT 1; /* a; SELECT 2", []string{"SELECT 1"}},
		{"NestedBlockComment", "SELECT 1 /* a /* b; */ c; */; SELECT 2", []string{"SELECT 1 /* a /* b; */ c; */", "SELECT 2"}},
		{"CommentedOutStatements", "/*\nSELECT 1; /* a */\nSELECT 2;\n*/\nSELECT 3;", []string{"/*\nSELECT 1; /* a */\nSELECT 2;\n*/\nSELECT 3"}},
		{"CommentOnlyStatementsDropped", "-- a\n; /* b */; SELECT 1; -- tail", []string{"SELECT 1"}},
		{"LeadingComment", "-- load\nINSERT INTO t VALUES (1); SELECT 2", []string{"-- load\nINSERT INTO t VALUES (1)", "SELECT 2"}},
		{"CommentMarkerInString", "SELECT '-- not; a comment'; SELECT 2", []string{"SELECT '-- not; a comment'", "SELECT 2"}},
		{"BlockMarkerInString", "SELECT '/* x'; SELECT '*/'", []string{"SELECT '/* x'", "SELECT '*/'"}},
		{"Heredoc", "SELECT $$a;b$$; SELECT 2", []string{"SELECT $$a;b$$", "SELECT 2"}},
		{"TaggedHeredoc", "SELECT $q$a;$$b$q$; SELECT 2", []string{"SELECT $q$a;$$b$q$", "SELECT 2"}},
		{"DollarInIdentifier", "SELECT a$b; SELECT 2", []string{"SELECT a$b", "SELECT 2"}},
		{"UnterminatedString", "SELECT 1; SELECT 'a;b", []string{"SELECT 1", "SELECT 'a;b"}},
		{"WindowsLineEndings", "SELECT 1;\r\nSELECT 2;\r\n", []string{"SELECT 1", "SELECT 2"}},
		{"Unicode", "SELECT 'é;ü'; SELECT 'ß'", []string{"SELECT 'é;ü'", "SELECT 'ß'"}},
		{"Placeholder", "SELECT {a:UInt8}; SELECT {b:String}", []string{"SELECT {a:UInt8}", "SELECT {b:String}"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, splitStatements(tt.script))
		})
	}
}

func TestSplitStatements_LargeScript(t *testing.T) {
	t.Parallel()

	script := strings.Repeat("INSERT INTO t VALUES (1, 'a;b');\n", 5000)
	assert.Len(t, splitStatements(script), 5000)
}

// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestString_DollarEscape(t *testing.T) {
	ctx := context.Background()
	opts := []Option{WithVariables(map[string]string{
		"HOME": "/tmp/home",
		"REAL": "value",
	})}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "BasicEscape",
			input:    `\$HOME`,
			expected: "$HOME",
		},
		{
			name:     "PriceLiteral",
			input:    `Price: \$9.99`,
			expected: "Price: $9.99",
		},
		{
			name:     "PasswordLiteral",
			input:    `p@ss\$word`,
			expected: "p@ss$word",
		},
		{
			name:     "EscapedBraces",
			input:    `\${REAL}`,
			expected: "${REAL}",
		},
		{
			name:     "MixedEscapeAndExpand",
			input:    `\$HOME is $HOME`,
			expected: "$HOME is /tmp/home",
		},
		{
			name:     "EvenBackslashesDontEscape",
			input:    `\\$HOME`,
			expected: "\\\\/tmp/home",
		},
		{
			name:     "OddBackslashesEscape",
			input:    `\\\$HOME`,
			expected: "\\\\$HOME",
		},
		{
			name:     "RegularExpand",
			input:    "$HOME",
			expected: "/tmp/home",
		},
		{
			name:     "SingleQuotedPreserved",
			input:    "'$HOME'",
			expected: "'$HOME'",
		},
		{
			name:     "SingleQuotedBracedPreserved",
			input:    "'${REAL}'",
			expected: "'${REAL}'",
		},
		{
			name:     "SingleQuotedPositionalPreserved",
			input:    "'$1'",
			expected: "'$1'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := String(ctx, tt.input, opts...)
			require.NoError(t, err)
			require.Equal(t, tt.expected, got)
		})
	}
}

func TestString_DollarEscape_Backtick(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping backtick tests on Windows")
	}
	ctx := context.Background()

	got, err := String(ctx, "`echo \\$`")
	require.NoError(t, err)
	require.Equal(t, "$", got)
}

func TestString_DollarEscape_Disabled(t *testing.T) {
	ctx := context.Background()

	got, err := String(ctx, `Price: \$9.99`, WithoutDollarEscape())
	require.NoError(t, err)
	require.Equal(t, `Price: \$9.99`, got)
}

func TestWithDollarEscapes_NoEscapes(t *testing.T) {
	ctx := context.Background()
	input := "no dollar escapes here"

	nextCtx, result := withDollarEscapes(ctx, input)
	require.Equal(t, ctx, nextCtx)
	require.Equal(t, input, result)
}

func TestWithDollarEscapes_BackslashesWithoutEscape(t *testing.T) {
	ctx := context.Background()
	input := `\\foo \\$BAR`

	nextCtx, result := withDollarEscapes(ctx, input)
	require.Equal(t, input, result)
	require.Equal(t, input, unescapeDollars(nextCtx, result))
}

func TestWithDollarEscapes_NilContext(t *testing.T) {
	ctx, result := withDollarEscapes(nil, `\$test`) //nolint:staticcheck // intentionally testing nil context handling
	require.NotNil(t, ctx)
	require.NotEqual(t, `\$test`, result)
	require.Equal(t, "$test", unescapeDollars(ctx, result))
}

func TestUnescapeDollars_NilContext(t *testing.T) {
	result := unescapeDollars(nil, `test\$value`) //nolint:staticcheck // intentionally testing nil context handling
	require.Equal(t, `test\$value`, result)
}

func TestUnescapeDollars_NoTokensInContext(t *testing.T) {
	ctx := context.Background()
	require.Equal(t, `test\$value`, unescapeDollars(ctx, `test\$value`))

	type otherKey struct{}
	ctx = context.WithValue(ctx, otherKey{}, "something")
	require.Equal(t, `test\$value`, unescapeDollars(ctx, `test\$value`))
}

func TestUnescapeDollars_EmptyTokens(t *testing.T) {
	ctx := context.WithValue(context.Background(), dollarEscapeKey{}, dollarEscapeToken{
		token: "",
	})
	require.Equal(t, `test\$value`, unescapeDollars(ctx, `test\$value`))
}

func TestUniqueToken_Fallback(t *testing.T) {
	const base = "__DAGU_DOLLAR_ESC__"
	const maxTokenAttempts = 1024

	prev := atomic.LoadUint64(&dollarEscapeSeq)
	atomic.StoreUint64(&dollarEscapeSeq, 0)
	t.Cleanup(func() {
		atomic.StoreUint64(&dollarEscapeSeq, prev)
	})

	var b strings.Builder
	for i := 1; i <= maxTokenAttempts; i++ {
		fmt.Fprintf(&b, "%s%d__", base, i)
	}
	input := b.String()

	token := uniqueToken(input, base)
	require.True(t, strings.HasSuffix(token, "__fallback__"))
	require.False(t, strings.Contains(input, token))
}

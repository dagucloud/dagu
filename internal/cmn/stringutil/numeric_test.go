// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package stringutil_test

import (
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
)

func TestHasNumericPrefix(t *testing.T) {
	tests := []struct {
		pattern string
		want    bool
	}{
		{"num:>=0.8", true},
		{"num:", true},
		{"re:.*", false},
		{"0.8", false},
		{" num:>=0.8", false},
	}

	for _, tt := range tests {
		if got := stringutil.HasNumericPrefix(tt.pattern); got != tt.want {
			t.Errorf("HasNumericPrefix(%q) = %v, want %v", tt.pattern, got, tt.want)
		}
	}
}

func TestParseNumericPattern(t *testing.T) {
	valid := []string{
		"num:>0.8",
		"num:>=0.8",
		"num:<0.8",
		"num:<=0.8",
		"num:>= 0.8",
		"num: >= 0.8 ",
		"num:>=-1",
		"num:>=+1",
		"num:>=0",
		"num:>=1e3",
		// The number is a Go floating-point literal, so the forms below are
		// accepted. "1e-400" underflows to zero rather than failing.
		"num:>=1_000.5",
		"num:>=0x1p-2",
		"num:>=1e-400",
	}
	for _, pattern := range valid {
		if _, err := stringutil.ParseNumericPattern(pattern); err != nil {
			t.Errorf("ParseNumericPattern(%q) returned error: %v", pattern, err)
		}
	}

	invalid := []string{
		"0.8",       // no prefix
		"re:.*",     // wrong prefix
		"num:",      // empty comparison
		"num:   ",   // whitespace-only comparison
		"num:0.8",   // no operator
		"num:==0.8", // equality is not supported
		"num:!=0.8",
		"num:=0.8",
		"num:=>0.8",
		"num:>=abc",
		"num:>=",
		"num:>=0.8extra",
		"num:>=NaN",
		"num:>=Inf",
		"num:>=-Inf",
		"num:>=1e400", // parses, but no 64-bit float holds it
		"num:>=-1e400",
	}
	for _, pattern := range invalid {
		if _, err := stringutil.ParseNumericPattern(pattern); err == nil {
			t.Errorf("ParseNumericPattern(%q) did not return an error", pattern)
		}
	}
}

// A number too large to hold parses fine, so reporting it as text that is not a
// number sends the author looking for the wrong mistake.
func TestParseNumericPatternOutOfRange(t *testing.T) {
	_, err := stringutil.ParseNumericPattern("num:>=1e400")
	if err == nil {
		t.Fatal("ParseNumericPattern(\"num:>=1e400\") did not return an error")
	}
	if !strings.Contains(err.Error(), "out of range") {
		t.Errorf("ParseNumericPattern(\"num:>=1e400\") returned %q, want it to report the number is out of range", err)
	}
}

func TestNumericComparisonMatch(t *testing.T) {
	tests := []struct {
		pattern string
		value   string
		want    bool
	}{
		{"num:>=0.8", "0.8", true},
		{"num:>=0.8", "0.87", true},
		{"num:>=0.8", "0.5", false},
		{"num:>0.8", "0.8", false},
		{"num:<=0.1", "0.1", true},
		{"num:<=0.1", "0.2", false},
		{"num:<0.1", "0.05", true},
		{"num:>=0.8", " 0.87 ", true},
		{"num:>=0.8", "0.87\n", true},
		{"num:>=100", "1e3", true},
		{"num:>=-1", "-0.5", true},
		{"num:>=0", "+0.5", true},
	}

	for _, tt := range tests {
		comparison, err := stringutil.ParseNumericPattern(tt.pattern)
		if err != nil {
			t.Fatalf("ParseNumericPattern(%q) returned error: %v", tt.pattern, err)
		}
		got, err := comparison.Match(tt.value)
		if err != nil {
			t.Errorf("Match(%q) against %q returned error: %v", tt.value, tt.pattern, err)
			continue
		}
		if got != tt.want {
			t.Errorf("Match(%q) against %q = %v, want %v", tt.value, tt.pattern, got, tt.want)
		}
	}
}

// A value that is not a single finite number is an error, never a false match,
// so that callers can fail loudly instead of silently treating it as not met.
func TestNumericComparisonMatchNotANumber(t *testing.T) {
	values := []string{
		"abc",
		"",
		"   ",
		"0.8abc",
		"0.5\n0.9", // numeric matching is not line-based
		"NaN",
		"Inf",
		"-Inf",
	}

	comparison, err := stringutil.ParseNumericPattern("num:>=0.8")
	if err != nil {
		t.Fatalf("ParseNumericPattern returned error: %v", err)
	}
	for _, value := range values {
		if _, err := comparison.Match(value); err == nil {
			t.Errorf("Match(%q) did not return an error", value)
		}
	}
}

func TestSplitNumericPattern(t *testing.T) {
	tests := []struct {
		pattern      string
		wantOperator string
		wantOperand  string
	}{
		{"num:>0.8", ">", "0.8"},
		{"num:>=0.8", ">=", "0.8"},
		{"num:<0.8", "<", "0.8"},
		{"num:<=0.8", "<=", "0.8"},
		{"num:>= 0.8 ", ">=", "0.8"},
		// The operand is returned as written so a caller can resolve it.
		{"num:>=${threshold}", ">=", "${threshold}"},
		{"num:>= ${params.threshold}", ">=", "${params.threshold}"},
		{"num:>=abc", ">=", "abc"},
	}

	for _, tt := range tests {
		operator, operand, err := stringutil.SplitNumericPattern(tt.pattern)
		if err != nil {
			t.Errorf("SplitNumericPattern(%q) returned error: %v", tt.pattern, err)
			continue
		}
		if operator != tt.wantOperator || operand != tt.wantOperand {
			t.Errorf("SplitNumericPattern(%q) = %q, %q, want %q, %q",
				tt.pattern, operator, operand, tt.wantOperator, tt.wantOperand)
		}
	}

	for _, pattern := range []string{"0.8", "re:.*", "num:", "num:   ", "num:0.8", "num:==0.8", "num:=>0.8"} {
		if _, _, err := stringutil.SplitNumericPattern(pattern); err == nil {
			t.Errorf("SplitNumericPattern(%q) did not return an error", pattern)
		}
	}
}

// A reference operand is not a number, so the strict parser still rejects it.
// Resolution happens before ParseNumericPattern is reached.
func TestParseNumericPatternRejectsReference(t *testing.T) {
	for _, pattern := range []string{"num:>=${threshold}", "num:>=${params.threshold}"} {
		if _, err := stringutil.ParseNumericPattern(pattern); err == nil {
			t.Errorf("ParseNumericPattern(%q) did not return an error", pattern)
		}
	}
}

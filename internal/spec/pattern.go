// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
)

// validateMatchPattern checks a value-match pattern: an exact string, a
// "re:"-prefixed regular expression, or a "num:"-prefixed numeric comparison.
//
// The returned error names what is wrong with the pattern; callers add the
// field it came from.
func validateMatchPattern(pattern string) error {
	if source, ok := stringutil.TrimRegexPrefix(pattern); ok {
		if strings.TrimSpace(source) == "" {
			return fmt.Errorf("regexp is empty")
		}
		if _, err := regexp.Compile(source); err != nil {
			return fmt.Errorf("regexp is invalid: %w", err)
		}
		return nil
	}

	if stringutil.HasNumericPrefix(pattern) {
		return validateNumericPattern(pattern)
	}

	return nil
}

// validateNumericPattern checks a numeric comparison as far as it can be checked
// before a run.
//
// The operator is always required. The operand must be a literal number unless
// it is a single whole value reference, whose number is only known once the
// reference resolves. Whether that reference has a value is not decided here: an
// unresolved one is reported as a notice, not rejected.
func validateNumericPattern(pattern string) error {
	_, operand, err := stringutil.SplitNumericPattern(pattern)
	if err != nil {
		return fmt.Errorf("numeric comparison is invalid: %w", err)
	}

	if cmnvalue.IsWholeReference(operand) {
		return nil
	}

	if _, err := stringutil.ParseNumericPattern(pattern); err != nil {
		return fmt.Errorf("numeric comparison is invalid: %w", err)
	}
	return nil
}

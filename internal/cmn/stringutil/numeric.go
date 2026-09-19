// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package stringutil

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

const numPrefix = "num:"

type numericOperator string

const (
	numericGreater      numericOperator = ">"
	numericGreaterEqual numericOperator = ">="
	numericLess         numericOperator = "<"
	numericLessEqual    numericOperator = "<="
)

// Ordered longest first so that ">=" is preferred over ">".
var numericOperators = []numericOperator{
	numericGreaterEqual,
	numericLessEqual,
	numericGreater,
	numericLess,
}

// NumericComparison is an ordering test against a fixed number.
type NumericComparison struct {
	op      numericOperator
	operand float64
}

// HasNumericPrefix reports whether pattern selects numeric comparison.
func HasNumericPrefix(pattern string) bool {
	return strings.HasPrefix(pattern, numPrefix)
}

// ParseNumericPattern parses a numeric-comparison pattern.
//
// It returns an error when pattern does not carry the numeric prefix, or when
// the remaining text is not one of the ordering operators ">", ">=", "<", or
// "<=" followed by a finite number. Equality operators are not supported.
func ParseNumericPattern(pattern string) (NumericComparison, error) {
	rest, ok := strings.CutPrefix(pattern, numPrefix)
	if !ok {
		return NumericComparison{}, fmt.Errorf("pattern %q does not start with %q", pattern, numPrefix)
	}

	rest = strings.TrimSpace(rest)
	if rest == "" {
		return NumericComparison{}, fmt.Errorf("comparison is empty")
	}

	for _, op := range numericOperators {
		operand, ok := strings.CutPrefix(rest, string(op))
		if !ok {
			continue
		}
		value, err := parseFiniteFloat(operand)
		if err != nil {
			return NumericComparison{}, fmt.Errorf("operator %q needs a number: %w", op, err)
		}
		return NumericComparison{op: op, operand: value}, nil
	}

	return NumericComparison{}, fmt.Errorf(
		"comparison %q must start with one of >, >=, <, <=; use an exact match to test equality", rest)
}

// Match reports whether value satisfies the comparison.
//
// Value is the whole text, not a line of it: surrounding whitespace is ignored
// and everything else must form a single finite number. It returns an error
// when value is not such a number.
func (c NumericComparison) Match(value string) (bool, error) {
	actual, err := parseFiniteFloat(value)
	if err != nil {
		return false, err
	}

	switch c.op {
	case numericGreater:
		return actual > c.operand, nil
	case numericGreaterEqual:
		return actual >= c.operand, nil
	case numericLess:
		return actual < c.operand, nil
	case numericLessEqual:
		return actual <= c.operand, nil
	default:
		return false, fmt.Errorf("unsupported operator %q", c.op)
	}
}

// parseFiniteFloat rejects the non-finite values strconv accepts, such as "NaN"
// and "Inf", because no ordering test against them is meaningful.
func parseFiniteFloat(text string) (float64, error) {
	trimmed := strings.TrimSpace(text)
	value, err := strconv.ParseFloat(trimmed, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a number", trimmed)
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("%q is not a finite number", trimmed)
	}
	return value, nil
}

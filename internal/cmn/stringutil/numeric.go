// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package stringutil

import (
	"errors"
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

// SplitNumericPattern separates a numeric-comparison pattern into its ordering
// operator and the text of its operand.
//
// The operand is returned as written apart from surrounding whitespace, so a
// caller can resolve a value reference in it before requiring a number. It
// returns an error when pattern does not carry the numeric prefix or does not
// continue with one of the ordering operators ">", ">=", "<", or "<=".
// Equality operators are not supported.
func SplitNumericPattern(pattern string) (operator, operand string, err error) {
	rest, ok := strings.CutPrefix(pattern, numPrefix)
	if !ok {
		return "", "", fmt.Errorf("pattern %q does not start with %q", pattern, numPrefix)
	}

	rest = strings.TrimSpace(rest)
	if rest == "" {
		return "", "", fmt.Errorf("comparison is empty")
	}

	for _, op := range numericOperators {
		if operand, ok := strings.CutPrefix(rest, string(op)); ok {
			return string(op), strings.TrimSpace(operand), nil
		}
	}

	return "", "", fmt.Errorf(
		"comparison %q must start with one of >, >=, <, <=; use an exact match to test equality", rest)
}

// ParseNumericPattern parses a numeric-comparison pattern whose operand is a
// literal number.
//
// It returns an error when the pattern is not a supported comparison or its
// operand is not a finite number. A pattern whose operand is a value reference
// must be resolved before it reaches this function.
func ParseNumericPattern(pattern string) (NumericComparison, error) {
	operator, operand, err := SplitNumericPattern(pattern)
	if err != nil {
		return NumericComparison{}, err
	}

	value, err := parseFiniteFloat(operand)
	if err != nil {
		return NumericComparison{}, fmt.Errorf("operator %q needs a number: %w", operator, err)
	}
	return NumericComparison{op: numericOperator(operator), operand: value}, nil
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
//
// The number is a Go floating-point literal, so a magnitude too large for a
// 64-bit float is a number that cannot be compared rather than text that is not
// a number, and says so.
func parseFiniteFloat(text string) (float64, error) {
	trimmed := strings.TrimSpace(text)
	value, err := strconv.ParseFloat(trimmed, 64)
	if err != nil {
		if errors.Is(err, strconv.ErrRange) {
			return 0, fmt.Errorf("%q is out of range", trimmed)
		}
		return 0, fmt.Errorf("%q is not a number", trimmed)
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("%q is not a finite number", trimmed)
	}
	return value, nil
}

// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Resources contains resource requests for a DAG run.
type Resources struct {
	Limits *ResourceLimits `json:"limits,omitempty"`
}

// ResourceLimits contains CPU and memory limits requested for a DAG run.
type ResourceLimits struct {
	CPU    string `json:"cpu,omitempty"`
	Memory string `json:"memory,omitempty"`

	CPUMillis   int64 `json:"-"`
	MemoryBytes int64 `json:"-"`
}

// Clone returns a deep copy of the resource configuration.
func (r *Resources) Clone() *Resources {
	if r == nil {
		return nil
	}
	clone := *r
	if r.Limits != nil {
		limits := *r.Limits
		clone.Limits = &limits
	}
	return &clone
}

// HasLimits reports whether at least one resource limit is configured.
func (r *Resources) HasLimits() bool {
	if r == nil || r.Limits == nil {
		return false
	}
	return r.Limits.CPUMillis > 0 || r.Limits.MemoryBytes > 0
}

// NewResourceLimits validates authored resource limits and returns a normalized copy.
func NewResourceLimits(cpu, memory string) (*ResourceLimits, error) {
	limits := &ResourceLimits{
		CPU:    strings.TrimSpace(cpu),
		Memory: strings.TrimSpace(memory),
	}
	if limits.CPU == "" && limits.Memory == "" {
		return nil, nil
	}
	if err := limits.recompute(); err != nil {
		return nil, err
	}
	return limits, nil
}

// UnmarshalJSON decodes authored limits and restores their normalized values.
func (r *ResourceLimits) UnmarshalJSON(data []byte) error {
	type resourceLimits ResourceLimits
	var decoded resourceLimits
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*r = ResourceLimits(decoded)
	return r.recompute()
}

func (r *ResourceLimits) recompute() error {
	r.CPU = strings.TrimSpace(r.CPU)
	r.Memory = strings.TrimSpace(r.Memory)
	r.CPUMillis = 0
	r.MemoryBytes = 0

	if r.CPU != "" {
		millis, err := ParseCPULimit(r.CPU)
		if err != nil {
			return fmt.Errorf("invalid cpu limit %q: %w", r.CPU, err)
		}
		r.CPUMillis = millis
	}
	if r.Memory != "" {
		bytes, err := ParseMemoryLimit(r.Memory)
		if err != nil {
			return fmt.Errorf("invalid memory limit %q: %w", r.Memory, err)
		}
		r.MemoryBytes = bytes
	}
	return nil
}

// ParseCPULimit parses Kubernetes-style CPU quantities into millicores.
func ParseCPULimit(value string) (int64, error) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return 0, fmt.Errorf("value is empty")
	}

	if before, ok := strings.CutSuffix(raw, "m"); ok {
		millis, err := parsePositiveInt64(before)
		if err != nil {
			return 0, err
		}
		return millis, nil
	}

	millis, err := parseCPUCoresToMillis(raw)
	if err != nil {
		return 0, err
	}
	return millis, nil
}

// ParseMemoryLimit parses Kubernetes-style memory quantities into bytes.
func ParseMemoryLimit(value string) (int64, error) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return 0, fmt.Errorf("value is empty")
	}

	number, suffix := splitQuantity(raw)
	amount, err := strconv.ParseFloat(number, 64)
	if err != nil {
		return 0, err
	}
	if amount <= 0 {
		return 0, fmt.Errorf("value must be greater than zero")
	}

	multiplier, ok := memoryMultiplier(suffix)
	if !ok {
		return 0, fmt.Errorf("unknown suffix %q", suffix)
	}
	bytes := amount * multiplier
	if bytes > float64(math.MaxInt64) {
		return 0, fmt.Errorf("value is too large")
	}
	rounded := int64(math.Round(bytes))
	if rounded <= 0 {
		return 0, fmt.Errorf("value is too small")
	}
	return rounded, nil
}

func splitQuantity(value string) (string, string) {
	idx := len(value)
	for idx > 0 {
		c := value[idx-1]
		if (c >= '0' && c <= '9') || c == '.' {
			break
		}
		idx--
	}
	return value[:idx], value[idx:]
}

func parseCPUCoresToMillis(value string) (int64, error) {
	whole, frac, hasFrac := strings.Cut(value, ".")
	if whole == "" || !isDigits(whole) {
		return 0, fmt.Errorf("invalid core value")
	}
	if hasFrac {
		if frac == "" || len(frac) > 3 || !isDigits(frac) {
			return 0, fmt.Errorf("cpu precision must be no finer than 1m")
		}
	}

	wholeValue, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, err
	}
	if wholeValue > math.MaxInt64/1000 {
		return 0, fmt.Errorf("value is too large")
	}

	millis := wholeValue * 1000
	if hasFrac {
		frac = frac + strings.Repeat("0", 3-len(frac))
		fracValue, err := strconv.ParseInt(frac, 10, 64)
		if err != nil {
			return 0, err
		}
		millis += fracValue
	}
	if millis <= 0 {
		return 0, fmt.Errorf("value must be greater than zero")
	}
	return millis, nil
}

func parsePositiveInt64(value string) (int64, error) {
	if value == "" || !isDigits(value) {
		return 0, fmt.Errorf("value must be a positive integer")
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, err
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("value must be greater than zero")
	}
	return parsed, nil
}

func isDigits(value string) bool {
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return value != ""
}

func memoryMultiplier(suffix string) (float64, bool) {
	switch strings.ToLower(strings.TrimSpace(suffix)) {
	case "":
		return 1, true
	case "b":
		return 1, true
	case "k", "kb":
		return 1_000, true
	case "m", "mb":
		return 1_000_000, true
	case "g", "gb":
		return 1_000_000_000, true
	case "t", "tb":
		return 1_000_000_000_000, true
	case "p", "pb":
		return 1_000_000_000_000_000, true
	case "e", "eb":
		return 1_000_000_000_000_000_000, true
	case "ki", "kib":
		return 1024, true
	case "mi", "mib":
		return 1024 * 1024, true
	case "gi", "gib":
		return 1024 * 1024 * 1024, true
	case "ti", "tib":
		return 1024 * 1024 * 1024 * 1024, true
	case "pi", "pib":
		return 1024 * 1024 * 1024 * 1024 * 1024, true
	case "ei", "eib":
		return 1024 * 1024 * 1024 * 1024 * 1024 * 1024, true
	default:
		return 0, false
	}
}

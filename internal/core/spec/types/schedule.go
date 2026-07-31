// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package types

import (
	"fmt"

	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/goccy/go-yaml"
)

// ScheduleValue represents a schedule configuration that can be specified as:
// - A single cron expression string
// - An array of cron expressions
// - A map with start/stop/restart keys
//
// YAML examples:
//
//	schedule: "0 * * * *"
//	schedule: ["0 * * * *", "30 * * * *"]
//	schedule:
//	  start: "0 8 * * *"
//	  stop: "0 18 * * *"
//	  restart: "0 12 * * *"
type ScheduleValue struct {
	raw      any             // Original value for error reporting
	isSet    bool            // Whether the field was set in YAML
	starts   []core.Schedule // Start schedules (or simple schedule expressions)
	stops    []core.Schedule // Stop schedules
	restarts []core.Schedule // Restart schedules
}

// UnmarshalYAML implements BytesUnmarshaler for goccy/go-yaml.
func (s *ScheduleValue) UnmarshalYAML(data []byte) error {
	s.isSet = true

	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("schedule unmarshal error: %w", err)
	}
	s.raw = raw

	switch v := raw.(type) {
	case string:
		if v == "" {
			return nil
		}
		schedule, err := core.ParseScheduleValue(v, core.ScheduleParseOptions{AllowAt: true})
		if err != nil {
			return fmt.Errorf("schedule: %w", err)
		}
		s.starts = []core.Schedule{schedule}
		return nil

	case []any:
		for i, item := range v {
			schedule, err := core.ParseScheduleValue(item, core.ScheduleParseOptions{AllowAt: true})
			if err != nil {
				return fmt.Errorf("schedule[%d]: %w", i, err)
			}
			s.starts = append(s.starts, schedule)
		}
		return nil

	case []core.Schedule:
		s.starts = v
		return nil
	case []string:
		for i, item := range v {
			schedule, err := core.ParseScheduleValue(item, core.ScheduleParseOptions{AllowAt: true})
			if err != nil {
				return fmt.Errorf("schedule[%d]: %w", i, err)
			}
			s.starts = append(s.starts, schedule)
		}
		return nil

	case map[string]any:
		return s.parseScheduleMap(v)

	case nil:
		s.isSet = false
		return nil

	default:
		return fmt.Errorf("schedule must be string, array, or map, got %T", v)
	}
}

func (s *ScheduleValue) parseScheduleMap(m map[string]any) error {
	for key, v := range m {
		var (
			target *[]core.Schedule
			opts   core.ScheduleParseOptions
		)
		switch key {
		case "start":
			target = &s.starts
			opts.AllowAt = true
		case "stop":
			target = &s.stops
		case "restart":
			target = &s.restarts
		default:
			return fmt.Errorf("schedule: unknown key %q (expected start, stop, or restart)", key)
		}

		values, err := parseScheduleEntry(v, opts)
		if err != nil {
			return fmt.Errorf("schedule.%s: %w", key, err)
		}
		*target = values
	}
	return nil
}

func parseScheduleEntry(v any, opts core.ScheduleParseOptions) ([]core.Schedule, error) {
	switch val := v.(type) {
	case string:
		schedule, err := core.ParseScheduleValue(val, opts)
		if err != nil {
			return nil, err
		}
		return []core.Schedule{schedule}, nil
	case []any:
		var result []core.Schedule
		for i, item := range val {
			schedule, err := core.ParseScheduleValue(item, opts)
			if err != nil {
				return nil, fmt.Errorf("[%d]: %w", i, err)
			}
			result = append(result, schedule)
		}
		return result, nil
	case []string:
		var result []core.Schedule
		for i, item := range val {
			schedule, err := core.ParseScheduleValue(item, opts)
			if err != nil {
				return nil, fmt.Errorf("[%d]: %w", i, err)
			}
			result = append(result, schedule)
		}
		return result, nil
	case map[string]any:
		schedule, err := core.ParseScheduleValue(val, opts)
		if err != nil {
			return nil, err
		}
		return []core.Schedule{schedule}, nil
	default:
		return nil, fmt.Errorf("expected string, object, or array, got %T", v)
	}
}

// IsZero returns true if the schedule was not set in YAML.
func (s ScheduleValue) IsZero() bool { return !s.isSet }

// Value returns the original raw value for error reporting.
func (s ScheduleValue) Value() any { return s.raw }

// Starts returns the start/simple schedules.
func (s ScheduleValue) Starts() []core.Schedule { return s.starts }

// Stops returns the stop schedules.
func (s ScheduleValue) Stops() []core.Schedule { return s.stops }

// Restarts returns the restart schedules.
func (s ScheduleValue) Restarts() []core.Schedule { return s.restarts }

// HasStopSchedule returns true if stop schedules are configured.
func (s ScheduleValue) HasStopSchedule() bool { return len(s.stops) > 0 }

// HasRestartSchedule returns true if restart schedules are configured.
func (s ScheduleValue) HasRestartSchedule() bool { return len(s.restarts) > 0 }

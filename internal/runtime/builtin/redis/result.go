// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package redis

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	goredis "github.com/redis/go-redis/v9"
)

// ResultWriter writes Redis command results in various formats.
type ResultWriter struct {
	w         io.Writer
	format    string
	nullValue string
	csvWriter *csv.Writer
}

// NewResultWriter creates a new result writer.
func NewResultWriter(w io.Writer, format, nullValue string) *ResultWriter {
	rw := &ResultWriter{
		w:         w,
		format:    format,
		nullValue: nullValue,
	}
	if format == "csv" {
		rw.csvWriter = csv.NewWriter(w)
	}
	return rw
}

// Write writes a result value to the output.
func (rw *ResultWriter) Write(value any) error {
	normalized := rw.normalize(value)

	switch rw.format {
	case "json":
		return rw.writeJSON(normalized)
	case "jsonl":
		return rw.writeJSONL(normalized)
	case "raw":
		return rw.writeRaw(normalized)
	case "csv":
		return rw.writeCSV(normalized)
	default:
		return rw.writeJSON(normalized)
	}
}

// writeJSON writes the value as formatted JSON.
func (rw *ResultWriter) writeJSON(value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal json: %w", err)
	}
	_, err = rw.w.Write(data)
	if err != nil {
		return err
	}
	_, err = rw.w.Write([]byte("\n"))
	return err
}

// writeJSONL writes the value as JSON Lines (one JSON per line).
func (rw *ResultWriter) writeJSONL(value any) error {
	// For arrays/slices, write each element on its own line
	switch v := value.(type) {
	case []any:
		for _, item := range v {
			if err := rw.writeSingleJSONL(item); err != nil {
				return err
			}
		}
		return nil
	case []string:
		for _, item := range v {
			if err := rw.writeSingleJSONL(item); err != nil {
				return err
			}
		}
		return nil
	case map[string]string:
		// For hash results, write each field-value pair
		for k, val := range v {
			if err := rw.writeSingleJSONL(map[string]string{k: val}); err != nil {
				return err
			}
		}
		return nil
	default:
		return rw.writeSingleJSONL(value)
	}
}

// writeSingleJSONL writes a single value as a JSON line.
func (rw *ResultWriter) writeSingleJSONL(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("failed to marshal jsonl: %w", err)
	}
	_, err = rw.w.Write(data)
	if err != nil {
		return err
	}
	_, err = rw.w.Write([]byte("\n"))
	return err
}

// writeRaw writes the value as raw text.
func (rw *ResultWriter) writeRaw(value any) error {
	switch v := value.(type) {
	case nil:
		_, err := fmt.Fprintln(rw.w, rw.nullValue)
		return err
	case string:
		_, err := fmt.Fprintln(rw.w, v)
		return err
	case []byte:
		_, err := rw.w.Write(v)
		if err != nil {
			return err
		}
		_, err = rw.w.Write([]byte("\n"))
		return err
	case []string:
		for _, s := range v {
			if _, err := fmt.Fprintln(rw.w, s); err != nil {
				return err
			}
		}
		return nil
	case []any:
		for _, item := range v {
			if err := rw.writeRaw(item); err != nil {
				return err
			}
		}
		return nil
	case map[string]string:
		for k, val := range v {
			if _, err := fmt.Fprintf(rw.w, "%s: %s\n", k, val); err != nil {
				return err
			}
		}
		return nil
	default:
		_, err := fmt.Fprintf(rw.w, "%v\n", v)
		return err
	}
}

// writeCSV writes the value as CSV.
func (rw *ResultWriter) writeCSV(value any) error {
	switch v := value.(type) {
	case nil:
		return rw.csvWriter.Write([]string{rw.nullValue})
	case string:
		return rw.csvWriter.Write([]string{v})
	case []string:
		return rw.csvWriter.Write(v)
	case []any:
		row := make([]string, len(v))
		for i, item := range v {
			row[i] = rw.formatValue(item)
		}
		return rw.csvWriter.Write(row)
	case map[string]string:
		// Write key-value pairs as two-column CSV
		for k, val := range v {
			if err := rw.csvWriter.Write([]string{k, val}); err != nil {
				return err
			}
		}
		return nil
	case []goredis.Z:
		// Sorted set with scores
		for _, z := range v {
			if err := rw.csvWriter.Write([]string{
				rw.formatValue(z.Member),
				fmt.Sprintf("%g", z.Score),
			}); err != nil {
				return err
			}
		}
		return nil
	default:
		return rw.csvWriter.Write([]string{rw.formatValue(v)})
	}
}

// Flush flushes any buffered data.
func (rw *ResultWriter) Flush() error {
	if rw.csvWriter != nil {
		rw.csvWriter.Flush()
		return rw.csvWriter.Error()
	}
	return nil
}

// normalize converts Redis-specific types to standard Go types.
func (rw *ResultWriter) normalize(value any) any {
	switch v := value.(type) {
	case nil:
		return nil
	case []goredis.Z:
		// Convert sorted set results
		result := make([]map[string]any, len(v))
		for i, z := range v {
			result[i] = map[string]any{
				"member": z.Member,
				"score":  z.Score,
			}
		}
		return result
	case []goredis.XMessage:
		// Convert stream messages
		result := make([]map[string]any, len(v))
		for i, msg := range v {
			result[i] = map[string]any{
				"id":     msg.ID,
				"values": msg.Values,
			}
		}
		return result
	case []goredis.XStream:
		// Convert stream results
		result := make([]map[string]any, len(v))
		for i, stream := range v {
			messages := make([]map[string]any, len(stream.Messages))
			for j, msg := range stream.Messages {
				messages[j] = map[string]any{
					"id":     msg.ID,
					"values": msg.Values,
				}
			}
			result[i] = map[string]any{
				"stream":   stream.Stream,
				"messages": messages,
			}
		}
		return result
	case []any:
		// Recursively normalize array elements
		result := make([]any, len(v))
		for i, item := range v {
			result[i] = rw.normalize(item)
		}
		return result
	default:
		return v
	}
}

// formatValue formats a value as a string for CSV output.
func (rw *ResultWriter) formatValue(v any) string {
	if v == nil {
		return rw.nullValue
	}
	switch val := v.(type) {
	case string:
		return val
	case []byte:
		return string(val)
	case fmt.Stringer:
		return val.String()
	default:
		return fmt.Sprintf("%v", v)
	}
}

// FormatDuration formats a duration for display.
func FormatDuration(d int64) string {
	if d < 0 {
		return "no expiry"
	}
	return fmt.Sprintf("%d seconds", d)
}

// TruncateString truncates a string to maxLen with ellipsis.
func TruncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return stringutil.TruncUTF8Bytes(s, maxLen)
	}
	return stringutil.TruncUTF8Bytes(s, maxLen-3) + "..."
}

// SanitizeKey removes newlines and control characters from a key for display.
func SanitizeKey(key string) string {
	key = strings.ReplaceAll(key, "\n", "\\n")
	key = strings.ReplaceAll(key, "\r", "\\r")
	key = strings.ReplaceAll(key, "\t", "\\t")
	return key
}

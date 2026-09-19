// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package jq

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/go-viper/mapstructure/v2"
	"github.com/itchyny/gojq"
)

var _ executor.Executor = (*jq)(nil)

type jq struct {
	stdout    io.Writer
	stderr    io.Writer
	query     string
	input     any
	cfg       *jqConfig
	variables []string
	values    []any
}

type jqConfig struct {
	Raw   bool           `mapstructure:"raw"`
	Input string         `mapstructure:"input"`
	Args  map[string]any `mapstructure:"args"`
}

func newJQ(ctx context.Context, step ir.Step) (executor.Executor, error) {
	var jqCfg jqConfig
	if step.ExecutorConfig.Config != nil {
		if err := decodeJqConfig(
			step.ExecutorConfig.Config, &jqCfg,
		); err != nil {
			return nil, err
		}
	}
	var input any
	switch {
	case jqCfg.Input != "" && step.Script != "":
		return nil, fmt.Errorf("jq: config.input and script are mutually exclusive; provide one, not both")
	case jqCfg.Input != "":
		// Evaluate the input path to resolve step references like ${step.stdout}
		inputPath, err := runtime.ResolveString(ctx, jqCfg.Input, cmnvalue.WorkflowField("jq.input"))
		if err != nil {
			return nil, fmt.Errorf("jq: failed to evaluate config.input: %w", err)
		}
		data, err := fileutil.ReadFile(inputPath)
		if err != nil {
			return nil, fmt.Errorf("jq: reading input file %q: %w", inputPath, err)
		}
		if err := json.Unmarshal(data, &input); err != nil {
			return nil, fmt.Errorf("jq: parsing JSON from input file %q: %w", inputPath, err)
		}
	case step.Script != "":
		if after, ok := strings.CutPrefix(step.Script, "file://"); ok {
			data, err := fileutil.ReadFile(after)
			if err != nil {
				return nil, fmt.Errorf("jq: reading input file %q: %w", after, err)
			}
			if err := json.Unmarshal(data, &input); err != nil {
				return nil, fmt.Errorf("jq: parsing JSON from file %q: %w", after, err)
			}
		} else {
			if err := json.Unmarshal([]byte(step.Script), &input); err != nil {
				return nil, err
			}
		}
	default:
		return nil, fmt.Errorf("jq: no input provided (set config.input to a file path, or script to inline JSON)")
	}

	// Extract query from Commands field
	var query string
	if len(step.Commands) > 0 {
		query = step.Commands[0].CmdWithArgs
	}

	variables := make([]string, 0, len(jqCfg.Args))
	values := make([]any, 0, len(jqCfg.Args))
	seen := make(map[string]struct{}, len(jqCfg.Args))
	for _, name := range sortedKeys(jqCfg.Args) {
		value := jqCfg.Args[name]
		variable := "$" + strings.TrimPrefix(name, "$")
		if _, dup := seen[variable]; dup {
			return nil, fmt.Errorf("jq: args %q duplicates variable %s", name, variable)
		}
		seen[variable] = struct{}{}
		variables = append(variables, variable)
		values = append(values, normalizeArgValue(value))
	}

	return &jq{
		stdout:    os.Stdout,
		input:     input,
		query:     query,
		cfg:       &jqCfg,
		variables: variables,
		values:    values,
	}, nil
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// normalizeArgValue converts decoded YAML values into the value types gojq
// accepts (nil, bool, int, float64, *big.Int, string, []any, map[string]any).
// YAML decoders produce types like uint64 that gojq cannot handle.
func normalizeArgValue(v any) any {
	switch v := v.(type) {
	case nil, bool, int, float64, string:
		return v
	case int8:
		return int(v)
	case int16:
		return int(v)
	case int32:
		return int(v)
	case int64:
		if v >= math.MinInt && v <= math.MaxInt {
			return int(v)
		}
		return new(big.Int).SetInt64(v)
	case uint:
		if v <= uint(math.MaxInt) {
			return int(v)
		}
		return new(big.Int).SetUint64(uint64(v))
	case uint8:
		return int(v)
	case uint16:
		return int(v)
	case uint32:
		return normalizeArgValue(uint64(v))
	case uint64:
		if v <= uint64(math.MaxInt) {
			return int(v)
		}
		return new(big.Int).SetUint64(v)
	case float32:
		return float64(v)
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = normalizeArgValue(item)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, item := range v {
			out[k] = normalizeArgValue(item)
		}
		return out
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprint(v)
		}
		var out any
		if err := json.Unmarshal(data, &out); err != nil {
			return fmt.Sprint(v)
		}
		return out
	}
}

func (e *jq) SetStdout(out io.Writer) {
	e.stdout = out
}

func (e *jq) SetStderr(out io.Writer) {
	e.stderr = out
}

func (*jq) Kill(_ os.Signal) error {
	return nil
}

func (e *jq) Run(_ context.Context) error {
	query, err := gojq.Parse(e.query)
	if err != nil {
		return err
	}
	code, err := gojq.Compile(query, gojq.WithVariables(e.variables))
	if err != nil {
		return err
	}
	iter := code.Run(e.input, e.values...)
	for {
		v, ok := iter.Next()
		if !ok {
			break
		}
		if err, ok := v.(error); ok {
			_, _ = fmt.Fprintf(e.stderr, "failed to run jq query: %v", err)
			continue
		}
		if e.cfg.Raw {
			// In raw mode, output values without JSON encoding
			switch v := v.(type) {
			case string:
				// For strings, print directly without quotes
				_, _ = fmt.Fprintln(e.stdout, v)
			case nil:
				// For null, print nothing or empty line
				_, _ = fmt.Fprintln(e.stdout)
			case bool:
				// For booleans, print as lowercase string
				if v {
					_, _ = fmt.Fprintln(e.stdout, "true")
				} else {
					_, _ = fmt.Fprintln(e.stdout, "false")
				}
			case float64:
				// For numbers, print without quotes
				_, _ = fmt.Fprintln(e.stdout, strconv.FormatFloat(v, 'f', -1, 64))
			default:
				// For arrays/objects or other types, marshal to JSON
				val, err := json.Marshal(v)
				if err != nil {
					_, _ = fmt.Fprintf(e.stderr, "failed to marshal jq output: %v", err)
					continue
				}
				// If the JSON is a quoted string, unquote it
				output := string(val)
				if len(output) >= 2 && output[0] == '"' && output[len(output)-1] == '"' {
					var unquoted string
					if err := json.Unmarshal(val, &unquoted); err == nil {
						_, _ = fmt.Fprintln(e.stdout, unquoted)
					} else {
						_, _ = fmt.Fprintln(e.stdout, output)
					}
				} else {
					_, _ = fmt.Fprintln(e.stdout, output)
				}
			}
		} else {
			// In non-raw mode, use JSON formatting
			val, err := json.MarshalIndent(v, "", "    ")
			if err != nil {
				_, _ = fmt.Fprintf(e.stderr, "failed to marshal jq output: %v", err)
				continue
			}
			_, _ = e.stdout.Write(val)
			_, _ = e.stdout.Write([]byte("\n"))
		}
	}
	return nil
}

func decodeJqConfig(dat map[string]any, cfg *jqConfig) error {
	md, _ := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		WeaklyTypedInput: true,
		ErrorUnused:      false,
		Result:           cfg,
	})
	return md.Decode(dat)
}

func init() {
	executor.RegisterExecutor("jq", newJQ, nil, registry.ExecutorCapabilities{Command: true, Script: true})
}

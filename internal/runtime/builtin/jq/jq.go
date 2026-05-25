// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package jq

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/runtime/executor"
	"github.com/go-viper/mapstructure/v2"
	"github.com/itchyny/gojq"
)

var _ executor.Executor = (*jq)(nil)

type jq struct {
	stdout io.Writer
	stderr io.Writer
	query  string
	input  any
	cfg    *jqConfig
}

type jqConfig struct {
	Raw   bool   `mapstructure:"raw"`
	Input string `mapstructure:"input"`
}

func newJQ(ctx context.Context, step core.Step) (executor.Executor, error) {
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
		inputPath, err := runtime.EvalStepString(ctx, jqCfg.Input)
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

	return &jq{
		stdout: os.Stdout,
		input:  input,
		query:  query,
		cfg:    &jqCfg,
	}, nil
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
	iter := query.Run(e.input)
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
	executor.RegisterExecutor("jq", newJQ, nil, core.ExecutorCapabilities{Command: true, Script: true})
}

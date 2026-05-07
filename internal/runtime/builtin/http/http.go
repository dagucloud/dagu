// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package http

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/runtime/executor"
	"github.com/go-resty/resty/v2"
	"github.com/go-viper/mapstructure/v2"
)

var _ executor.Executor = (*http)(nil)

type http struct {
	stdout    io.Writer
	stderr    io.Writer
	req       *resty.Request
	reqCancel context.CancelFunc
	url       string
	method    string
	cfg       *httpConfig
}

type httpConfig struct {
	Method        string            `json:"method" mapstructure:"method"`
	URL           string            `json:"url" mapstructure:"url"`
	Timeout       int               `json:"timeout" mapstructure:"timeout"`
	Headers       map[string]string `json:"headers" mapstructure:"headers"`
	Query         map[string]string `json:"query" mapstructure:"query"`
	Body          string            `json:"body" mapstructure:"body"`
	Silent        bool              `json:"silent" mapstructure:"silent"`
	Debug         bool              `json:"debug" mapstructure:"debug"`
	Format        string            `json:"format" mapstructure:"format"`
	JSON          bool              `json:"json" mapstructure:"json"`
	SkipTLSVerify bool              `json:"skip_tls_verify" mapstructure:"skip_tls_verify"`
}

type httpJSONResult struct {
	StatusCode int                 `json:"status_code"`
	Headers    map[string][]string `json:"headers"`
	Body       any                 `json:"body"`
}

func newHTTP(ctx context.Context, step core.Step) (executor.Executor, error) {
	var reqCfg httpConfig
	if len(step.Script) > 0 {
		if err := decodeHTTPConfigFromString(ctx, step.Script, &reqCfg); err != nil {
			return nil, err
		}
	} else if step.ExecutorConfig.Config != nil {
		if err := decodeHTTPConfig(
			step.ExecutorConfig.Config, &reqCfg,
		); err != nil {
			return nil, err
		}
	}

	// Extract method and url from Commands field.
	// Prefer CmdWithArgs (fully expanded) over Command/Args (not expanded)
	// so that parameter variables in the method position are resolved.
	var method string
	var url string
	if len(step.Commands) > 0 {
		cmd := step.Commands[0]
		if cmd.CmdWithArgs != "" {
			method, url = splitMethodURL(cmd.CmdWithArgs)
		} else {
			method = cmd.Command
			if len(cmd.Args) > 0 {
				url = cmd.Args[0]
			}
		}
	} else {
		method = step.Command
		if len(step.Args) > 0 {
			url = step.Args[0]
		}
	}
	if method == "" {
		method = reqCfg.Method
	}
	if url == "" {
		url = reqCfg.URL
	}

	ctx, cancel := context.WithCancel(ctx)

	client := resty.New()
	if reqCfg.Debug {
		client.SetDebug(true)
	}
	if reqCfg.Timeout > 0 {
		client.SetTimeout(time.Second * time.Duration(reqCfg.Timeout))
	}
	if reqCfg.SkipTLSVerify {
		client.SetTLSClientConfig(&tls.Config{
			InsecureSkipVerify: true, // nolint:gosec
		})
	}
	req := client.R().SetContext(ctx)
	if len(reqCfg.Headers) > 0 {
		req = req.SetHeaders(reqCfg.Headers)
	}
	if len(reqCfg.Query) > 0 {
		req = req.SetQueryParams(reqCfg.Query)
	}
	req = req.SetBody([]byte(reqCfg.Body))

	return &http{
		stdout:    os.Stdout,
		stderr:    os.Stderr,
		req:       req,
		reqCancel: cancel,
		method:    method,
		url:       url,
		cfg:       &reqCfg,
	}, nil
}

func (e *http) SetStdout(out io.Writer) {
	e.stdout = out
}

func (e *http) SetStderr(out io.Writer) {
	e.stderr = out
}

func (e *http) Kill(_ os.Signal) error {
	e.reqCancel()
	return nil
}

var errHTTPStatusCode = errors.New("http status code not 2xx")

func (e *http) writeJSONResult(rsp *resty.Response) error {
	var (
		httpJSONResultData  = &httpJSONResult{}
		err                 error
		httpJSONResultBytes []byte
	)

	if !rsp.IsSuccess() || !e.cfg.Silent {
		httpJSONResultData.Headers = rsp.Header()
		httpJSONResultData.StatusCode = rsp.StatusCode()
	}

	if err = json.Unmarshal(rsp.Body(), &httpJSONResultData.Body); err != nil {
		return err
	}

	if httpJSONResultBytes, err = json.MarshalIndent(httpJSONResultData, "", " "); err != nil {
		return err
	}

	if _, err = e.stdout.Write(httpJSONResultBytes); err != nil {
		return err
	}

	return nil
}

func (e *http) writeTextResult(rsp *resty.Response) error {
	if !rsp.IsSuccess() || !e.cfg.Silent {
		if _, err := e.stdout.Write([]byte(rsp.Status() + "\n")); err != nil {
			return err
		}
		if err := rsp.Header().Write(e.stdout); err != nil {
			return err
		}
	}

	if _, err := e.stdout.Write(rsp.Body()); err != nil {
		return err
	}

	return nil
}

func (e *http) isJSONFormat() bool {
	return e.cfg.Format == "json" || e.cfg.JSON
}

func (e *http) Run(_ context.Context) error {
	rsp, err := e.req.Execute(strings.ToUpper(e.method), e.url)
	if err != nil {
		return err
	}

	resCode := rsp.StatusCode()

	if e.isJSONFormat() {
		if err = e.writeJSONResult(rsp); err != nil {
			return err
		}
	} else {
		if err = e.writeTextResult(rsp); err != nil {
			return err
		}
	}

	if !rsp.IsSuccess() {
		return fmt.Errorf("%w: %d", errHTTPStatusCode, resCode)
	}
	return nil
}

func decodeHTTPConfig(dat map[string]any, cfg *httpConfig) error {
	md, _ := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		WeaklyTypedInput: true,
		ErrorUnused:      false,
		Result:           cfg,
	})
	return md.Decode(dat)
}

func decodeHTTPConfigFromString(_ context.Context, source string, target *httpConfig) error {
	if len(source) > 0 {
		if err := json.Unmarshal([]byte(source), target); err != nil {
			return err
		}
	}
	return nil
}

// splitMethodURL splits "METHOD URL" into method and URL parts.
func splitMethodURL(cmdWithArgs string) (string, string) {
	parts := strings.SplitN(strings.TrimSpace(cmdWithArgs), " ", 2)
	if len(parts) == 1 {
		return parts[0], ""
	}
	return parts[0], strings.TrimSpace(parts[1])
}

func init() {
	executor.RegisterExecutor("http", newHTTP, nil, core.ExecutorCapabilities{Command: true, Script: true})
}

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
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/go-resty/resty/v2"
	"github.com/go-viper/mapstructure/v2"
)

var (
	_ executor.Executor  = (*http)(nil)
	_ executor.ExitCoder = (*http)(nil)
)

type http struct {
	stdout    io.Writer
	stderr    io.Writer
	req       *resty.Request
	reqCancel context.CancelFunc
	url       string
	method    string
	cfg       *httpConfig
	exitCode  int
}

type httpConfig struct {
	Method        string            `json:"method" mapstructure:"method"`
	URL           string            `json:"url" mapstructure:"url"`
	Timeout       int               `json:"timeout" mapstructure:"timeout"`
	Headers       map[string]string `json:"headers" mapstructure:"headers"`
	Query         map[string]string `json:"query" mapstructure:"query"`
	Body          string            `json:"body" mapstructure:"body"`
	Form          map[string]string `json:"form" mapstructure:"form"`
	Files         map[string]string `json:"files" mapstructure:"files"`
	Silent        bool              `json:"silent" mapstructure:"silent"`
	Debug         bool              `json:"debug" mapstructure:"debug"`
	Format        string            `json:"format" mapstructure:"format"`
	Output        string            `json:"output" mapstructure:"output"`
	JSON          bool              `json:"json" mapstructure:"json"`
	SkipTLSVerify bool              `json:"skip_tls_verify" mapstructure:"skip_tls_verify"`
	// StatusExitCodes maps a non-2xx status code or class (e.g. "404", "5xx")
	// to the step exit code. An exact code wins over a class. A status mapped
	// to 0 is a success; an unmapped one exits 1.
	StatusExitCodes map[string]int `json:"status_exit_codes,omitzero" mapstructure:"status_exit_codes"`
}

// httpJSONResult is the stdout of format: json. A silent success leaves
// StatusCode and Headers unset, so the output holds only the body.
type httpJSONResult struct {
	StatusCode int                 `json:"status_code,omitzero"`
	Headers    map[string][]string `json:"headers,omitzero"`
	Body       any                 `json:"body,omitempty"`
	Output     string              `json:"output,omitempty"`
}

func newHTTP(ctx context.Context, step ir.Step) (executor.Executor, error) {
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
	if err := validateStatusExitCodes(reqCfg.StatusExitCodes); err != nil {
		return nil, err
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
	if url == "" {
		return nil, fmt.Errorf("http executor: url is required (set via command, args, or with.url)")
	}
	if method == "" {
		return nil, fmt.Errorf("http executor: method is required (set via command or with.method)")
	}
	if reqCfg.Body != "" && (len(reqCfg.Form) > 0 || len(reqCfg.Files) > 0) {
		return nil, fmt.Errorf("http executor: body cannot be combined with form or files")
	}
	if err := prepareHTTPOutputConfig(ctx, &reqCfg); err != nil {
		return nil, err
	}
	prepareHTTPFilePaths(ctx, &reqCfg)

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
	if len(reqCfg.Form) > 0 {
		req = req.SetMultipartFormData(reqCfg.Form)
	}
	if len(reqCfg.Files) > 0 {
		req = req.SetFiles(reqCfg.Files)
	}
	if len(reqCfg.Form) == 0 && len(reqCfg.Files) == 0 {
		req = req.SetBody([]byte(reqCfg.Body))
	}
	if reqCfg.Output != "" {
		req = req.SetDoNotParseResponse(true)
	}

	return &http{
		stdout:    os.Stdout,
		stderr:    os.Stderr,
		req:       req,
		reqCancel: cancel,
		method:    method,
		url:       url,
		cfg:       &reqCfg,
		// A failure exits 1 unless status_exit_codes maps the response status.
		exitCode: 1,
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

func (e *http) writeJSONResult(rsp *resty.Response, body []byte) error {
	var (
		httpJSONResultData  = &httpJSONResult{}
		err                 error
		httpJSONResultBytes []byte
	)
	if body == nil {
		body = rsp.Body()
	}

	if !e.succeeded(rsp) || !e.cfg.Silent {
		httpJSONResultData.Headers = rsp.Header()
		httpJSONResultData.StatusCode = rsp.StatusCode()
	}

	if e.cfg.Output != "" && e.succeeded(rsp) {
		httpJSONResultData.Output = e.cfg.Output
	} else {
		if err = json.Unmarshal(body, &httpJSONResultData.Body); err != nil {
			// Only a successful response must carry JSON; a failed one keeps
			// its body as text so the step reports the status failure.
			if e.succeeded(rsp) {
				return err
			}
			httpJSONResultData.Body = string(body)
		} else if httpJSONResultData.Body == nil {
			// Keep a JSON null body as "body": null; omitempty drops a nil any.
			httpJSONResultData.Body = json.RawMessage("null")
		}
	}

	if httpJSONResultBytes, err = json.MarshalIndent(httpJSONResultData, "", " "); err != nil {
		return err
	}

	if _, err = e.stdout.Write(httpJSONResultBytes); err != nil {
		return err
	}

	return nil
}

func (e *http) writeTextPrefix(rsp *resty.Response) error {
	if !e.succeeded(rsp) || !e.cfg.Silent {
		if _, err := e.stdout.Write([]byte(rsp.Status() + "\n")); err != nil {
			return err
		}
		if err := rsp.Header().Write(e.stdout); err != nil {
			return err
		}
	}

	return nil
}

func (e *http) writeTextResult(rsp *resty.Response) error {
	if err := e.writeTextPrefix(rsp); err != nil {
		return err
	}
	if _, err := e.stdout.Write(rsp.Body()); err != nil {
		return err
	}

	return nil
}

func (e *http) writeTextResultFromReader(rsp *resty.Response, body io.Reader) error {
	if err := e.writeTextPrefix(rsp); err != nil {
		return err
	}
	_, err := io.Copy(e.stdout, body)
	return err
}

func (e *http) writeFileResult(rsp *resty.Response, body io.Reader) error {
	if !e.cfg.Silent && !e.isJSONFormat() {
		if err := e.writeTextPrefix(rsp); err != nil {
			return err
		}
	}

	return writeResponseBodyFile(e.cfg.Output, body)
}

func writeResponseBodyFile(output string, body io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(output), 0o750); err != nil {
		return err
	}
	tmpFile, err := os.CreateTemp(filepath.Dir(output), filepath.Base(output)+".tmp.*")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	cleanup := func() { _ = fileutil.Remove(tmpPath) }

	if _, err = io.Copy(tmpFile, body); err != nil {
		_ = tmpFile.Close()
		cleanup()
		return err
	}
	if err = tmpFile.Chmod(0o600); err != nil {
		_ = tmpFile.Close()
		cleanup()
		return err
	}
	if err = tmpFile.Close(); err != nil {
		cleanup()
		return err
	}
	if err = fileutil.ReplaceFile(tmpPath, output); err != nil {
		cleanup()
		return err
	}
	return nil
}

func (e *http) isJSONFormat() bool {
	return e.cfg.Format == "json" || e.cfg.JSON
}

func (e *http) hasFileOutput() bool {
	return e.cfg.Output != ""
}

func (e *http) Run(_ context.Context) error {
	rsp, err := e.req.Execute(strings.ToUpper(e.method), e.url)
	if err != nil {
		return err
	}

	resCode := rsp.StatusCode()

	if e.hasFileOutput() {
		if err = e.writeFileOutputResult(rsp); err != nil {
			return err
		}
		if !e.succeeded(rsp) {
			return e.statusError(resCode)
		}
		return nil
	}

	if e.isJSONFormat() {
		if err = e.writeJSONResult(rsp, rsp.Body()); err != nil {
			return err
		}
	} else {
		if err = e.writeTextResult(rsp); err != nil {
			return err
		}
	}

	if !e.succeeded(rsp) {
		return e.statusError(resCode)
	}
	return nil
}

// succeeded reports whether rsp has a 2xx status or one that status_exit_codes
// maps to 0.
func (e *http) succeeded(rsp *resty.Response) bool {
	if rsp.IsSuccess() {
		return true
	}
	code, ok := resolveStatusExitCode(e.cfg.StatusExitCodes, rsp.StatusCode())
	return ok && code == 0
}

// statusError returns the error for a failed response and records the exit
// code status_exit_codes maps its status to.
func (e *http) statusError(status int) error {
	if code, ok := resolveStatusExitCode(e.cfg.StatusExitCodes, status); ok {
		e.exitCode = code
	}
	return fmt.Errorf("%w: %d", errHTTPStatusCode, status)
}

// ExitCode implements executor.ExitCoder. It reports the exit code of a failed
// run: the status_exit_codes mapping of the response status, or 1.
func (e *http) ExitCode() int {
	return e.exitCode
}

func (e *http) writeFileOutputResult(rsp *resty.Response) error {
	body := rsp.RawBody()
	if body == nil {
		return errors.New("http executor: response body is unavailable")
	}
	defer func() { _ = body.Close() }()

	if e.succeeded(rsp) {
		if err := e.writeFileResult(rsp, body); err != nil {
			return err
		}
		if e.isJSONFormat() {
			return e.writeJSONResult(rsp, nil)
		}
		return nil
	}

	if e.isJSONFormat() {
		data, err := io.ReadAll(body)
		if err != nil {
			return err
		}
		return e.writeJSONResult(rsp, data)
	}

	return e.writeTextResultFromReader(rsp, body)
}

func prepareHTTPOutputConfig(ctx context.Context, cfg *httpConfig) error {
	output := strings.TrimSpace(cfg.Output)
	if output == "" {
		cfg.Output = ""
		return nil
	}
	if !filepath.IsAbs(output) {
		env := runtime.GetEnv(ctx)
		output = filepath.Join(env.WorkingDir, output)
	}
	cfg.Output = filepath.Clean(output)
	return nil
}

func prepareHTTPFilePaths(ctx context.Context, cfg *httpConfig) {
	for field, path := range cfg.Files {
		if !filepath.IsAbs(path) {
			path = filepath.Join(runtime.GetEnv(ctx).WorkingDir, path)
		}
		cfg.Files[field] = filepath.Clean(path)
	}
}

func decodeHTTPConfig(dat map[string]any, cfg *httpConfig) error {
	md, _ := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		WeaklyTypedInput: true,
		ErrorUnused:      false,
		Result:           cfg,
	})
	return md.Decode(dat)
}

// validateStatusExitCodes checks a status_exit_codes map. Keys must be a numeric
// status code (300-599) or a status class (3xx-5xx); values must be 0-255.
func validateStatusExitCodes(m map[string]int) error {
	for key, code := range m {
		if !validStatusKey(key) {
			return fmt.Errorf("http status_exit_codes: invalid key %q (use a code 300-599 or a class 3xx-5xx)", key)
		}
		if code < 0 || code > 255 {
			return fmt.Errorf("http status_exit_codes: value for %q must be 0-255, got %d", key, code)
		}
	}
	return nil
}

// validStatusKey reports whether key is a single status code (300-599) or a
// class (3xx-5xx).
func validStatusKey(key string) bool {
	if len(key) != 3 {
		return false
	}
	if key[1] == 'x' && key[2] == 'x' {
		return key[0] >= '3' && key[0] <= '5'
	}
	for _, c := range key {
		if c < '0' || c > '9' {
			return false
		}
	}
	n := int(key[0]-'0')*100 + int(key[1]-'0')*10 + int(key[2]-'0')
	return n >= 300 && n <= 599
}

// resolveStatusExitCode returns the configured exit code for status, preferring
// an exact code over a class (4xx, 5xx, ...). The second result reports whether
// a mapping was found.
func resolveStatusExitCode(m map[string]int, status int) (int, bool) {
	if m == nil {
		return 0, false
	}
	if code, ok := m[strconv.Itoa(status)]; ok {
		return code, true
	}
	class := strconv.Itoa(status/100) + "xx"
	if code, ok := m[class]; ok {
		return code, true
	}
	return 0, false
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
	executor.RegisterExecutor("http", newHTTP, nil, registry.ExecutorCapabilities{
		Command:    true,
		Script:     true,
		PathInputs: []string{"output", "files"},
	})
}

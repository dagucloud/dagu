// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package clickhouse

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	headerUser         = "X-ClickHouse-User"
	headerKey          = "X-ClickHouse-Key"
	headerExceptionTag = "X-ClickHouse-Exception-Tag"

	// maxErrorBody bounds the part of a failed response kept for the error.
	maxErrorBody = 8 << 10
)

// Cancellation tuning: a KILL QUERY request gets killTimeout in all and is
// sent up to killAttempts times, killRetryDelay apart.
const (
	killTimeout    = 5 * time.Second
	killAttempts   = 3
	killRetryDelay = 100 * time.Millisecond
)

// serverError is a failure reported by the server or by a proxy in front of it.
type serverError struct {
	status  int
	message string
}

func (e *serverError) Error() string {
	if e.status == http.StatusOK {
		return "failed after the result started streaming: " + e.message
	}
	return fmt.Sprintf("HTTP %d: %s", e.status, e.message)
}

// client sends statements to one ClickHouse HTTP endpoint.
type client struct {
	cfg       *Config
	base      *url.URL
	transport *http.Transport
	http      *http.Client

	killRetryDelay time.Duration
}

func newClient(cfg *Config, workDir string) (*client, error) {
	base, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid url: %w", redactURLError(err))
	}

	transport := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
		MaxIdleConnsPerHost: 1,
		IdleConnTimeout:     30 * time.Second,
		// The response is streamed as the server sends it; never negotiate
		// compression, which would also change how the body is read.
		DisableCompression: true,
	}
	if cfg.TLSSkipVerify || cfg.TLSCA != "" {
		tlsCfg := &tls.Config{InsecureSkipVerify: cfg.TLSSkipVerify} //nolint:gosec // explicit opt-in
		if cfg.TLSCA != "" {
			pool, err := loadCAPool(cfg.TLSCA, workDir)
			if err != nil {
				return nil, err
			}
			tlsCfg.RootCAs = pool
		}
		transport.TLSClientConfig = tlsCfg
	}

	return &client{
		cfg:            cfg,
		base:           base,
		killRetryDelay: killRetryDelay,
		transport:      transport,
		http: &http.Client{
			Transport: transport,
			// A redirect would replay the credential headers on the target.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

// loadCAPool returns the system roots plus the certificates in a PEM bundle.
func loadCAPool(path, workDir string) (*x509.CertPool, error) {
	if !filepath.IsAbs(path) && workDir != "" {
		path = filepath.Join(workDir, path)
	}
	pem, err := os.ReadFile(path) //nolint:gosec // the path is part of the step configuration
	if err != nil {
		return nil, fmt.Errorf("failed to read tls_ca: %w", err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(pem) {
		return nil, errors.New("tls_ca holds no PEM certificate")
	}
	return pool, nil
}

func (c *client) close() {
	c.transport.CloseIdleConnections()
}

// statement is one request to the server.
type statement struct {
	sql       string
	queryID   string
	sessionID string
	// inSession is set when the session already selected the configured
	// database with USE. The request then omits the database parameter, which
	// would override a later USE statement of the script.
	inSession bool
}

// outputSettings returns the default_format of the configured output format
// and any further settings it needs. A statement with its own FORMAT clause
// overrides the default.
func (c *client) outputSettings() (format string, extra map[string]string) {
	switch c.cfg.OutputFormat {
	case formatJSON:
		return "JSONEachRow", map[string]string{"output_format_json_array_of_rows": "1"}
	case formatCSV:
		if c.cfg.Headers {
			return "CSVWithNames", nil
		}
		return "CSV", nil
	default:
		return "JSONEachRow", nil
	}
}

// queryURL builds the URL of a statement request. Credentials never appear
// in it.
func (c *client) queryURL(st statement) (string, error) {
	q := url.Values{}
	format, extra := c.outputSettings()
	q.Set("default_format", format)
	for k, v := range extra {
		q.Set(k, v)
	}
	for k, v := range c.cfg.Settings {
		s, err := formatSetting(v)
		if err != nil {
			return "", fmt.Errorf("settings %q: %w", k, err)
		}
		q.Set(k, s)
	}
	for k, v := range c.cfg.Params {
		s, err := formatParam(v)
		if err != nil {
			return "", fmt.Errorf("params %q: %w", k, err)
		}
		q.Set(paramPrefix+k, s)
	}
	// Written after the user settings; a setting of the same name is rejected
	// by ParseConfig. An error written into the data would end the body
	// normally and hide the failure.
	q.Set("http_write_exception_in_output_format", "0")
	if c.cfg.Database != "" && !st.inSession {
		q.Set("database", c.cfg.Database)
	}
	q.Set("query_id", st.queryID)
	if st.sessionID != "" {
		q.Set("session_id", st.sessionID)
	}

	u := *c.base
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (c *client) newRequest(ctx context.Context, target, body string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(body))
	if err != nil {
		return nil, c.wrapError(err)
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	if c.cfg.User != "" {
		req.Header.Set(headerUser, c.cfg.User)
	}
	if c.cfg.Password != "" {
		req.Header.Set(headerKey, c.cfg.Password)
	}
	return req, nil
}

// execute sends one statement and streams the response body to out as it
// arrives. It returns the number of bytes written to out.
func (c *client) execute(ctx context.Context, st statement, out io.Writer) (int64, error) {
	target, err := c.queryURL(st)
	if err != nil {
		return 0, err
	}
	req, err := c.newRequest(ctx, target, st.sql)
	if err != nil {
		return 0, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, c.wrapError(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return 0, c.statusError(resp)
	}

	tag := resp.Header.Get(headerExceptionTag)
	if tag == "" {
		n, err := io.Copy(out, resp.Body)
		return n, c.wrapError(interrupted(err))
	}

	// The status line is sent before the first result bytes, so an error that
	// occurs later is reported inside the body, which the server then ends
	// without the terminating chunk. The read fails with an unexpected EOF
	// after the report was delivered, so the report takes precedence over the
	// read error.
	filter := newExceptionFilter(out, tag)
	_, readErr := io.Copy(filter, resp.Body)
	msg, failed, err := filter.finish()
	switch {
	case failed:
		return filter.written, &serverError{status: http.StatusOK, message: truncateText(c.redact(msg))}
	case readErr != nil:
		return filter.written, c.wrapError(interrupted(readErr))
	}
	return filter.written, err
}

// interrupted explains an unexpected end of the response body: a server that
// fails after it started streaming ends the body without the final chunk, and
// only recent servers add the error text before doing so.
func interrupted(err error) error {
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return fmt.Errorf("the response ended before it was complete, the server may have failed while streaming the result: %w", err)
	}
	return err
}

// statusError builds the error of a non-200 response.
func (c *client) statusError(resp *http.Response) error {
	// Read more than is kept so that a password cut by the limit is still
	// found and replaced before the text is truncated.
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8*maxErrorBody))
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		msg = http.StatusText(resp.StatusCode)
	}
	return &serverError{status: resp.StatusCode, message: truncateText(c.redact(msg))}
}

// kill asks the server to stop the query with the given id. It is best
// effort: the request runs on its own deadline, independent of ctx, and
// carries no session or settings, which the running query may still hold.
// The server may not have registered the query yet when the request arrives,
// so an empty answer is retried a few times.
func (c *client) kill(ctx context.Context, queryID string) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), killTimeout)
	defer cancel()

	q := url.Values{}
	q.Set(paramPrefix+"query_id", queryID)
	u := *c.base
	u.RawQuery = q.Encode()

	var lastErr error
	for attempt := range killAttempts {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return c.wrapError(ctx.Err())
			case <-time.After(c.killRetryDelay):
			}
		}
		req, err := c.newRequest(ctx, u.String(), "KILL QUERY WHERE query_id = {query_id:String}")
		if err != nil {
			return err
		}
		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = c.wrapError(err)
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return &serverError{status: resp.StatusCode, message: c.redact(strings.TrimSpace(string(body)))}
		}
		if len(bytes.TrimSpace(body)) > 0 {
			return nil
		}
		lastErr = errors.New("query not found on the server")
	}
	return lastErr
}

// wrapError returns err without the request URL, which carries parameter and
// setting values, and without the password.
func (c *client) wrapError(err error) error {
	if err == nil {
		return nil
	}
	if ue, ok := errors.AsType[*url.Error](err); ok {
		err = fmt.Errorf("%s %s: %w", ue.Op, c.endpoint(), ue.Err)
	}
	if c.cfg.Password != "" && strings.Contains(err.Error(), c.cfg.Password) {
		return errors.New(c.redact(err.Error()))
	}
	return err
}

// endpoint returns the server address for messages.
func (c *client) endpoint() string {
	return c.base.Scheme + "://" + c.base.Host + c.base.Path
}

// redact replaces the password in text.
func (c *client) redact(text string) string {
	if c.cfg.Password == "" {
		return text
	}
	return strings.ReplaceAll(text, c.cfg.Password, "***")
}

// exceptionFilter forwards a response body to out and diverts the report the
// server appends when a statement fails after the 200 status was sent. The
// report has the form
//
//	\r\n__exception__\r\n<tag>\r\n<message><length> <tag>\r\n__exception__\r\n
//
// where tag is the value of the X-ClickHouse-Exception-Tag response header,
// which is unique to the response, and length is the byte length of the
// message. Everything before the opening marker is forwarded at once; only a
// possible partial marker at the end of the data read so far is held back.
type exceptionFilter struct {
	out       io.Writer
	tag       string
	head      []byte // opening marker including the tag
	pending   []byte // trailing bytes that may begin a marker
	report    []byte // the report, once its opening marker was seen
	truncated bool   // report exceeded maxReport
	written   int64
	err       error
}

// maxReport bounds the memory held for an exception report.
const maxReport = 1 << 20

func newExceptionFilter(out io.Writer, tag string) *exceptionFilter {
	return &exceptionFilter{out: out, tag: tag, head: []byte("\r\n__exception__\r\n" + tag + "\r\n")}
}

func (w *exceptionFilter) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if w.report != nil {
		w.appendReport(p)
		return len(p), nil
	}

	data := p
	if len(w.pending) > 0 {
		data = append(w.pending, p...)
	}
	if i := bytes.Index(data, w.head); i >= 0 {
		if err := w.forward(data[:i]); err != nil {
			return 0, err
		}
		w.report = make([]byte, 0, len(data)-i)
		w.pending = nil
		w.appendReport(data[i:])
		return len(p), nil
	}

	hold := partialMarkerLen(data, w.head)
	if err := w.forward(data[:len(data)-hold]); err != nil {
		return 0, err
	}
	// data may be p, which the caller reuses; keep a copy of the held bytes.
	w.pending = append(w.pending[:0], data[len(data)-hold:]...)
	return len(p), nil
}

func (w *exceptionFilter) forward(b []byte) error {
	if len(b) == 0 {
		return w.err
	}
	n, err := w.out.Write(b)
	w.written += int64(n)
	w.err = err
	return err
}

func (w *exceptionFilter) appendReport(b []byte) {
	if room := maxReport - len(w.report); len(b) > room {
		b = b[:room]
		w.truncated = true
	}
	w.report = append(w.report, b...)
}

// finish ends the stream. It returns the exception message when the server
// reported one, and otherwise forwards what was held back.
func (w *exceptionFilter) finish() (message string, failed bool, err error) {
	if w.report == nil {
		return "", false, w.forward(w.pending)
	}
	return w.message(), true, nil
}

// message extracts the text of the exception report.
func (w *exceptionFilter) message() string {
	suffix := " " + w.tag + "\r\n__exception__\r\n"
	body := bytes.TrimPrefix(w.report, w.head)
	if !w.truncated && bytes.HasSuffix(body, []byte(suffix)) {
		body = body[:len(body)-len(suffix)]
		digits := 0
		for digits < len(body) && body[len(body)-1-digits] >= '0' && body[len(body)-1-digits] <= '9' {
			digits++
		}
		if n, err := strconv.Atoi(string(body[len(body)-digits:])); err == nil && digits > 0 {
			body = body[:len(body)-digits]
			if n <= len(body) {
				body = body[len(body)-n:]
			}
		}
	}
	return strings.TrimSpace(string(body))
}

// truncateText cuts text that would otherwise fill the step error, stderr, and
// the logs.
func truncateText(text string) string {
	if len(text) <= maxErrorBody {
		return text
	}
	return strings.ToValidUTF8(text[:maxErrorBody], "") + "... (truncated)"
}

// partialMarkerLen returns the length of the longest proper prefix of marker
// that ends data.
func partialMarkerLen(data, marker []byte) int {
	for n := min(len(marker)-1, len(data)); n > 0; n-- {
		if bytes.Equal(data[len(data)-n:], marker[:n]) {
			return n
		}
	}
	return 0
}

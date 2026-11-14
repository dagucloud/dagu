// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cloudrelay

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
)

// Relay frame types, as the relay and the server agree on them.
const (
	frameHello     = "hello"
	frameHooks     = "hooks"
	frameHooked    = "hooked"
	frameDelivery  = "delivery"
	frameDelivered = "delivered"
	frameMissed    = "missed"
	frameRequest   = "req"
	frameResponse  = "res"
	frameEnd       = "end"
	frameCancel    = "cancel"
)

// Binary chunks carry the bodies of calls: a version byte, the 16 bytes of
// the call's UUID, then its bytes.
const (
	chunkVersion     = 1
	chunkHeaderBytes = 17
	// chunkBytes bounds the bytes of an answer one chunk carries.
	chunkBytes = 64 << 10
	// callBodyLimit bounds a call's body; the relay sends at most 512 KiB.
	callBodyLimit = 1 << 20
)

// AssertionHeader carries, to Handler.Call, the assertion Dagu Cloud signed
// for the call, which the relay passes on unread.
const AssertionHeader = "Dagu-Assertion"

// instanceHeader names which of the server's processes opens a connection
// for calls.
const instanceHeader = "Dagu-Instance"

const (
	relayPing = "ping"
	relayPong = "pong"
	// Why the relay ends a connection: on schedule, every 12 hours; because
	// another connection from the same server took over; or because Dagu
	// Cloud disconnected the server.
	closeExpired  = "expired"
	closeReplaced = "replaced"
	closeDropped  = "dropped"
	// readLimit bounds one frame from the relay: a request body of 512 KiB,
	// escaped as JSON, with its headers.
	readLimit = 8 << 20
)

// timing is how a connection keeps itself known to be alive.
type timing struct {
	// pingInterval spaces the pings. The relay answers each without waking,
	// and takes a socket silent for 45 seconds for offline.
	pingInterval time.Duration
	// silence is how long the relay may say nothing before the connection
	// is taken for dead, as the relay takes the server.
	silence time.Duration
	// pongWait is how long a ping must have been out unanswered first, so a
	// ping held behind a long write is not blamed on the relay.
	pongWait time.Duration
	// watchInterval spaces the checks for an unanswered ping.
	watchInterval time.Duration
	// writeTimeout bounds one write, so a connection the network has
	// silently dropped cannot hold a write forever.
	writeTimeout time.Duration
}

var defaultTiming = timing{
	pingInterval:  15 * time.Second,
	silence:       45 * time.Second,
	pongWait:      30 * time.Second,
	watchInterval: 5 * time.Second,
	writeTimeout:  time.Minute,
}

var (
	// ErrSilent means the relay stopped answering.
	ErrSilent = errors.New("the relay stopped answering")
	// ErrReplaced means another connection from this server took over.
	ErrReplaced = errors.New("another connection from this server took over the relay")
	// ErrDropped means Dagu Cloud disconnected this server.
	ErrDropped = errors.New("the server was disconnected from Dagu Cloud")
)

// Hello is what the server tells the relay first.
type Hello struct {
	Name    string
	Version string
	// Instance names the process opening a connection for calls, which the
	// relay keeps beside the connections of the server's other processes. It
	// is empty for the scheduler's connection.
	Instance string
}

// Hook is one public webhook URL as the relay knows it: an ID the server
// chooses, the token its URL ends in, and the sender whose URL check the
// relay answers for it.
type Hook struct {
	ID    string
	Token string
	// Handshake names that sender: HandshakeSlack, or empty for none.
	Handshake string
}

// HandshakeSlack has the relay answer Slack's URL check, which needs its
// challenge back at once, even while the server is away.
const HandshakeSlack = "slack"

// Hooked is the relay's answer to a set of hooks: the URL they share, or why
// it refused the set.
type Hooked struct {
	URL string
	// Error is "too_many_hooks" when the set is larger than the ticket allows.
	Error string
}

// ErrorTooManyHooks is the relay's refusal of a set larger than the ticket
// allows.
const ErrorTooManyHooks = "too_many_hooks"

// Missed counts the webhooks the relay dropped for one hook: those that
// waited past its 7 days, and those it refused while too many waited.
type Missed struct {
	Hook    string `json:"hook"`
	Expired int    `json:"expired"`
	Refused int    `json:"refused"`
}

// Delivery is a webhook the relay kept for one of this server's hooks.
type Delivery struct {
	// ID tells this delivery apart from every other, across repeats.
	ID       string
	Hook     string
	Received time.Time
	Header   http.Header
	Body     []byte
}

// Outcome is what became of a delivery. The relay forgets a delivery on any
// outcome but a retry.
type Outcome struct {
	name string
	// after is how long a retried delivery waits at the relay.
	after time.Duration
}

var (
	OutcomeStarted = Outcome{name: "started"}
	OutcomeIgnored = Outcome{name: "ignored"}
	OutcomeFailed  = Outcome{name: "failed"}
)

// OutcomeRetry asks the relay to keep a delivery and send it again after the
// given time, holding back its hook's later deliveries until then.
func OutcomeRetry(after time.Duration) Outcome {
	return Outcome{name: "retry", after: after}
}

// String is the outcome's name on the wire.
func (o Outcome) String() string { return o.name }

// Handler serves what the relay sends to this server.
type Handler struct {
	// Delivery takes a webhook and reports what became of it. The relay
	// sends a webhook again on the next connection until it hears an
	// outcome, so the same delivery can arrive more than once.
	Delivery func(ctx context.Context, d Delivery) Outcome
	// Hooked receives the relay's answer to each set of hooks.
	Hooked func(h Hooked)
	// Missed receives, for each hook, how many webhooks the relay dropped
	// since it last told this server. Each count is told once.
	Missed func(missed []Missed)
	// Call serves the calls Dagu Cloud sends through the relay, each with its
	// assertion in AssertionHeader. A call the relay cancels has its
	// request's context canceled.
	Call http.Handler
}

// Conn is a connection this server holds open to the relay.
type Conn struct {
	ws     *websocket.Conn
	timing timing
	// reads counts the messages read from the relay, the latest at heard.
	// pingMark is what it was when the oldest unanswered ping left, at
	// pinged. Times are Unix nanoseconds.
	reads, heard, pingMark, pinged atomic.Int64
	// silent records that the watchdog closed the connection.
	silent atomic.Bool
	// hookSets numbers the hook sets sent.
	hookSets atomic.Int64
	// calls are the calls being received or served, by ID.
	calls sync.Map
}

// call is a call from the relay: its request frame and the body received so
// far, until its end arrives and it is served.
type call struct {
	req    frame
	ctx    context.Context
	cancel context.CancelFunc
	body   bytes.Buffer
	// tooLarge is set once the body passes callBodyLimit.
	tooLarge bool
	// ended is set, on the Serve goroutine, once the call's end arrives;
	// what arrives for the call after it is ignored.
	ended bool
}

// frame is one JSON message between the relay and the server.
type frame struct {
	Type    string            `json:"t"`
	ID      string            `json:"id,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    json.RawMessage   `json:"body,omitempty"`
	Name    string            `json:"name,omitempty"`
	Version string            `json:"version,omitempty"`
	// Hooks is a pointer so that an empty set is still sent.
	Hooks    *[]relayHook `json:"hooks,omitempty"`
	Hook     string       `json:"hook,omitempty"`
	Received string       `json:"received,omitempty"`
	URL      string       `json:"url,omitempty"`
	Error    string       `json:"error,omitempty"`
	Outcome  string       `json:"outcome,omitempty"`
	// After is how many seconds a retried delivery waits.
	After int `json:"after,omitempty"`
	// Method, Path, and Assertion are a call's; Status is its answer's.
	Method    string `json:"method,omitempty"`
	Path      string `json:"path,omitempty"`
	Assertion string `json:"assertion,omitempty"`
	Status    int    `json:"status,omitempty"`
}

// relayHook is a hook as the relay stores it: the token only as a digest.
type relayHook struct {
	Hook      string `json:"hook"`
	Digest    string `json:"digest"`
	Handshake string `json:"handshake,omitempty"`
}

// Dial opens a connection to the relay a ticket names and introduces this
// server.
func Dial(ctx context.Context, client *http.Client, ticket Ticket, hello Hello) (*Conn, error) {
	header := http.Header{"Authorization": {"Bearer " + ticket.Token}, "User-Agent": {userAgent()}}
	if hello.Instance != "" {
		header.Set(instanceHeader, hello.Instance)
	}
	ws, res, err := websocket.Dial(ctx, ticket.URL, &websocket.DialOptions{HTTPClient: client, HTTPHeader: header})
	if res != nil && res.Body != nil {
		_ = res.Body.Close()
	}
	if err != nil {
		if res != nil {
			return nil, fmt.Errorf("the relay refused this server (%d)", res.StatusCode)
		}
		return nil, fmt.Errorf("connect to the relay: %w", err)
	}
	ws.SetReadLimit(readLimit)
	conn := &Conn{ws: ws, timing: defaultTiming}
	conn.heard.Store(time.Now().UnixNano())
	if err := conn.send(ctx, frame{Type: frameHello, Name: hello.Name, Version: hello.Version}); err != nil {
		_ = ws.CloseNow()
		return nil, err
	}
	return conn, nil
}

// Close ends the connection without serving it.
func (c *Conn) Close() error { return c.ws.CloseNow() }

// SetHooks gives the relay the whole set of this server's hooks, replacing
// the set it holds. Webhooks waiting for a hook left out are dropped. The
// relay answers through Handler.Hooked.
func (c *Conn) SetHooks(ctx context.Context, hooks []Hook) error {
	set := make([]relayHook, 0, len(hooks))
	for _, h := range hooks {
		digest := sha256.Sum256([]byte(h.Token))
		set = append(set, relayHook{Hook: h.ID, Digest: base64.RawURLEncoding.EncodeToString(digest[:]), Handshake: h.Handshake})
	}
	return c.send(ctx, frame{Type: frameHooks, ID: strconv.FormatInt(c.hookSets.Add(1), 10), Hooks: &set})
}

// Serve answers what the relay sends until the connection ends or ctx is
// done. Deliveries run concurrently. It returns nil when the relay ended the
// connection on schedule.
func (c *Conn) Serve(ctx context.Context, h Handler) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer func() { _ = c.ws.CloseNow() }()
	go c.ping(ctx)
	go c.watch(ctx)
	for {
		kind, data, err := c.ws.Read(ctx)
		if err != nil {
			if c.silent.Load() {
				return ErrSilent
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return closed(err)
		}
		c.heard.Store(time.Now().UnixNano())
		c.reads.Add(1)
		if kind == websocket.MessageBinary {
			c.receiveChunk(data)
			continue
		}
		var f frame
		if string(data) == relayPong || json.Unmarshal(data, &f) != nil {
			continue
		}
		switch f.Type {
		case frameRequest:
			callCtx, cancel := context.WithCancel(ctx)
			c.calls.Store(f.ID, &call{req: f, ctx: callCtx, cancel: cancel})
		case frameEnd:
			if value, ok := c.calls.Load(f.ID); ok && !value.(*call).ended {
				cl := value.(*call)
				cl.ended = true
				go c.serveCall(h.Call, cl)
			}
		case frameCancel:
			if value, ok := c.calls.LoadAndDelete(f.ID); ok {
				value.(*call).cancel()
			}
		case frameDelivery:
			go func() {
				outcome := deliver(ctx, h.Delivery, f)
				// A delivery cut short by the connection ending is sent again
				// on the next one, so it gets no outcome here.
				if ctx.Err() == nil {
					after := int(math.Ceil(outcome.after.Seconds()))
					_ = c.send(ctx, frame{Type: frameDelivered, ID: f.ID, Outcome: outcome.String(), After: after})
				}
			}()
		case frameHooked:
			if h.Hooked != nil {
				h.Hooked(Hooked{URL: f.URL, Error: f.Error})
			}
		case frameMissed:
			var missed struct {
				Hooks []Missed `json:"hooks"`
			}
			if h.Missed != nil && json.Unmarshal(data, &missed) == nil && len(missed.Hooks) > 0 {
				h.Missed(missed.Hooks)
			}
		}
	}
}

// closed explains why the connection ended, or is nil when the relay ended
// it on schedule.
func closed(err error) error {
	closeErr, ok := errors.AsType[websocket.CloseError](err)
	switch {
	case !ok:
		return err
	case closeErr.Code == websocket.StatusNormalClosure && closeErr.Reason == closeExpired:
		return nil
	case closeErr.Reason == closeReplaced:
		return ErrReplaced
	case closeErr.Reason == closeDropped:
		return ErrDropped
	}
	return fmt.Errorf("the relay closed the connection (%d %s)", closeErr.Code, closeErr.Reason)
}

// ping pings the relay, recording when the oldest unanswered ping actually
// left. A ping waiting behind a long write is not counted until it leaves.
func (c *Conn) ping(ctx context.Context) {
	ticker := time.NewTicker(c.timing.pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		reads := c.reads.Load()
		answered := c.pinged.Load() == 0 || reads != c.pingMark.Load()
		if c.write(ctx, []byte(relayPing)) == nil && answered {
			c.pingMark.Store(reads)
			c.pinged.Store(time.Now().UnixNano())
		}
	}
}

// watch closes a connection the relay has said nothing on for silence, with
// a ping out unanswered for pongWait, which a network change or sleep can
// leave open on this side alone.
func (c *Conn) watch(ctx context.Context) {
	ticker := time.NewTicker(c.timing.watchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		pinged := c.pinged.Load()
		if pinged != 0 && c.reads.Load() == c.pingMark.Load() && time.Since(time.Unix(0, pinged)) > c.timing.pongWait &&
			time.Since(time.Unix(0, c.heard.Load())) > c.timing.silence {
			c.silent.Store(true)
			_ = c.ws.CloseNow()
			return
		}
	}
}

// receiveChunk adds a binary chunk to the body of the call it names.
func (c *Conn) receiveChunk(data []byte) {
	if len(data) < chunkHeaderBytes || data[0] != chunkVersion {
		return
	}
	id, err := uuid.FromBytes(data[1:chunkHeaderBytes])
	if err != nil {
		return
	}
	value, ok := c.calls.Load(id.String())
	if !ok {
		return
	}
	cl := value.(*call)
	if cl.ended {
		return
	}
	if cl.tooLarge || cl.body.Len()+len(data)-chunkHeaderBytes > callBodyLimit {
		cl.tooLarge = true
		cl.body.Reset()
		return
	}
	cl.body.Write(data[chunkHeaderBytes:])
}

// serveCall serves a call whose body arrived, and sends its answer: its
// status and headers, its body in chunks, and an end. A call the relay
// canceled gets no answer.
func (c *Conn) serveCall(handler http.Handler, cl *call) {
	defer c.calls.Delete(cl.req.ID)
	defer cl.cancel()
	w := &callWriter{conn: c, ctx: cl.ctx, id: cl.req.ID, header: http.Header{}}
	switch {
	case handler == nil:
		w.WriteHeader(http.StatusNotFound)
	case cl.tooLarge:
		w.WriteHeader(http.StatusRequestEntityTooLarge)
	default:
		r, err := http.NewRequestWithContext(cl.ctx, cl.req.Method, "http://dagu-cloud-relay"+cl.req.Path,
			bytes.NewReader(cl.body.Bytes()))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			break
		}
		for name, value := range cl.req.Headers {
			r.Header.Set(name, value)
		}
		r.Header.Set(AssertionHeader, cl.req.Assertion)
		r.RemoteAddr = "dagu-cloud-relay"
		handler.ServeHTTP(w, r)
	}
	w.finish()
}

// callWriter writes a call's answer to the relay as it comes.
type callWriter struct {
	conn    *Conn
	ctx     context.Context
	id      string
	header  http.Header
	started bool
	pending []byte
	err     error
}

func (w *callWriter) Header() http.Header { return w.header }

func (w *callWriter) WriteHeader(status int) {
	if w.started {
		return
	}
	w.started = true
	headers := make(map[string]string, len(w.header))
	for name, values := range w.header {
		if len(values) > 0 {
			headers[name] = values[0]
		}
	}
	w.fail(w.conn.send(w.ctx, frame{Type: frameResponse, ID: w.id, Status: status, Headers: headers}))
}

func (w *callWriter) Write(p []byte) (int, error) {
	if !w.started {
		w.WriteHeader(http.StatusOK)
	}
	if w.err != nil {
		return 0, w.err
	}
	w.pending = append(w.pending, p...)
	for len(w.pending) >= chunkBytes && w.err == nil {
		w.sendChunk(w.pending[:chunkBytes])
		w.pending = append([]byte(nil), w.pending[chunkBytes:]...)
	}
	return len(p), w.err
}

// Flush sends what the handler wrote so far.
func (w *callWriter) Flush() {
	if !w.started {
		w.WriteHeader(http.StatusOK)
	}
	if len(w.pending) > 0 && w.err == nil {
		w.sendChunk(w.pending)
		w.pending = nil
	}
}

// finish sends the rest of the answer and its end.
func (w *callWriter) finish() {
	w.Flush()
	if w.err == nil {
		w.fail(w.conn.send(w.ctx, frame{Type: frameEnd, ID: w.id}))
	}
}

func (w *callWriter) sendChunk(bytes []byte) {
	id, err := uuid.Parse(w.id)
	if err != nil {
		w.fail(err)
		return
	}
	chunk := make([]byte, 0, chunkHeaderBytes+len(bytes))
	chunk = append(chunk, chunkVersion)
	chunk = append(chunk, id[:]...)
	chunk = append(chunk, bytes...)
	ctx, cancel := context.WithTimeout(w.ctx, w.conn.timing.writeTimeout)
	defer cancel()
	w.fail(w.conn.ws.Write(ctx, websocket.MessageBinary, chunk))
}

func (w *callWriter) fail(err error) {
	if w.err == nil {
		w.err = err
	}
}

func (c *Conn) send(ctx context.Context, f frame) error {
	data, err := encode(f)
	if err != nil {
		return err
	}
	return c.write(ctx, data)
}

// write sends one message, giving up after the write timeout. A write that
// stops part way closes the connection.
func (c *Conn) write(ctx context.Context, data []byte) error {
	ctx, cancel := context.WithTimeout(ctx, c.timing.writeTimeout)
	defer cancel()
	return c.ws.Write(ctx, websocket.MessageText, data)
}

// encode encodes a frame as the relay reads it. HTML is not escaped, which
// would let a body grow sixfold.
func encode(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// deliver hands a delivery frame to take. One that cannot be read, or that
// nothing takes, is ignored.
func deliver(ctx context.Context, take func(context.Context, Delivery) Outcome, f frame) Outcome {
	var body string
	received, err := time.Parse(time.RFC3339Nano, f.Received)
	if take == nil || err != nil || json.Unmarshal(f.Body, &body) != nil {
		return OutcomeIgnored
	}
	header := http.Header{}
	for name, value := range f.Headers {
		header.Set(name, value)
	}
	return take(ctx, Delivery{ID: f.ID, Hook: f.Hook, Received: received, Header: header, Body: []byte(body)})
}

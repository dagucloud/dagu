// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package terminal

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"
	"github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/service/audit"
	"github.com/google/uuid"
)

const (
	defaultTerminalRows       = 24
	defaultTerminalCols       = 80
	auditTimeout              = 5 * time.Second
	processShutdownGrace      = 3 * time.Second
	processExitDetectionDelay = 250 * time.Millisecond
)

type terminalEndReason string

const (
	terminalEndReasonClosed      terminalEndReason = "closed"
	terminalEndReasonClientClose terminalEndReason = "client_close"
	terminalEndReasonDisconnect  terminalEndReason = "disconnect"
	terminalEndReasonShutdown    terminalEndReason = "shutdown"
	terminalEndReasonPTYError    terminalEndReason = "pty_error"
	terminalEndReasonShellExit   terminalEndReason = "shell_exit"
)

func (r terminalEndReason) String() string {
	if r == "" {
		return string(terminalEndReasonClosed)
	}
	return string(r)
}

type runEvent struct {
	reason        terminalEndReason
	sendOutput    string
	sendError     string
	gracefulClose bool
	err           error
}

type websocketOp uint8

const (
	websocketOpRead websocketOp = iota + 1
	websocketOpWrite
)

type websocketConn interface {
	Read(ctx context.Context) (websocket.MessageType, []byte, error)
	Write(ctx context.Context, typ websocket.MessageType, p []byte) error
	Close(status websocket.StatusCode, reason string) error
	CloseNow() error
}

type terminalErrorSource uint8

const (
	terminalErrorSourcePTYRead terminalErrorSource = iota + 1
	terminalErrorSourceWebSocketRead
	terminalErrorSourceWebSocketWrite
)

type terminalErrorDecision struct {
	suppress bool
	event    runEvent
}

type runState struct {
	ioWG        sync.WaitGroup
	processDone chan struct{}
	eventCh     chan runEvent
	eventOnce   sync.Once
}

func newRunState() *runState {
	return &runState{
		processDone: make(chan struct{}),
		eventCh:     make(chan runEvent, 1),
	}
}

func (s *runState) signal(ev runEvent) {
	s.eventOnce.Do(func() {
		s.eventCh <- ev
	})
}

// Connection represents an interactive terminal connection.
type Connection struct {
	ID         string
	User       *auth.User
	IPAddress  string
	Shell      string
	Conn       websocketConn
	StartTime  time.Time
	LastActive time.Time

	ptmx *os.File
	cmd  *exec.Cmd

	sendMu sync.Mutex
	state  sync.Mutex

	inputBuffer []byte // accumulates input until newline for command logging
	inEscSeq    bool   // true when processing an ANSI escape sequence
	closing     atomic.Bool

	// onSessionEnd is called when the session event loop exits, before
	// cleanup (process termination, I/O drain) begins. This allows the
	// caller to release resources (e.g., session lease) without waiting
	// for the potentially slow cleanup sequence.
	onSessionEnd func()
}

// NewConnection creates a new terminal connection.
func NewConnection(user *auth.User, shell string, conn *websocket.Conn, ipAddress string) *Connection {
	now := time.Now()
	return &Connection{
		ID:         uuid.Must(uuid.NewV7()).String(),
		User:       user,
		IPAddress:  ipAddress,
		Shell:      shell,
		Conn:       conn,
		StartTime:  now,
		LastActive: now,
	}
}

// Run starts the terminal connection and handles communication between
// the WebSocket and the PTY.
func (c *Connection) Run(ctx context.Context, auditSvc *audit.Service) (retErr error) {
	if ctx == nil {
		ctx = context.Background()
	}

	c.logConnectionStart(auditSvc)
	if err := c.startShell(); err != nil {
		c.sendError("Failed to start shell: " + err.Error())
		c.logConnectionEnd(auditSvc, terminalEndReasonPTYError)
		return err
	}

	sessionCtx, cancel := context.WithCancel(ctx)
	state := newRunState()
	event := runEvent{reason: terminalEndReasonClosed}

	defer func() {
		cleanupErr := c.cleanup(cancel, &state.ioWG, state.processDone, event.gracefulClose)
		c.logConnectionEnd(auditSvc, event.reason)
		if cleanupErr != nil {
			if retErr != nil {
				retErr = errors.Join(retErr, cleanupErr)
			} else {
				retErr = cleanupErr
			}
		}
	}()

	c.startRunLoops(sessionCtx, auditSvc, state)
	event = <-state.eventCh
	c.emitRunEvent(event)

	// Notify the caller that the session is done before the cleanup defer
	// runs. This allows the session lease to be released immediately,
	// without waiting for process termination and I/O drain.
	if c.onSessionEnd != nil {
		c.onSessionEnd()
	}

	return event.err
}

func (c *Connection) startShell() error {
	cmd := exec.Command(c.Shell) //nolint:gosec // shell path is from config, not user input
	cmd.Env = os.Environ()

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return err
	}

	c.cmd = cmd
	c.ptmx = ptmx
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: defaultTerminalRows, Cols: defaultTerminalCols})
	return nil
}

func (c *Connection) startRunLoops(ctx context.Context, auditSvc *audit.Service, state *runState) {
	state.ioWG.Add(2)
	go func() {
		defer state.ioWG.Done()
		c.readFromPTY(ctx, state.processDone, state.signal)
	}()
	go func() {
		defer state.ioWG.Done()
		c.readFromWebSocket(ctx, auditSvc, state.signal)
	}()
	go func() {
		waitErr := c.cmd.Wait()
		close(state.processDone)
		state.signal(classifyProcessExit(waitErr))
	}()
	go func() {
		<-ctx.Done()
		state.signal(runEvent{reason: terminalEndReasonShutdown})
	}()
}

func (c *Connection) emitRunEvent(event runEvent) {
	if event.sendError != "" {
		c.sendError(event.sendError)
	}
	if event.sendOutput != "" {
		c.sendOutput(event.sendOutput)
	}
}

// readFromPTY reads output from the PTY and sends it to the WebSocket.
func (c *Connection) readFromPTY(ctx context.Context, processDone <-chan struct{}, signal func(runEvent)) {
	buf := make([]byte, 4096)
	for {
		n, err := c.ptmx.Read(buf)
		if err != nil {
			decision := classifyTerminalError(ctx, err, terminalErrorSourcePTYRead, processDone, c.isClosing())
			if decision.suppress {
				return
			}
			signal(decision.event)
			return
		}

		if n == 0 {
			continue
		}

		writeCtx, cancel := context.WithTimeout(ctx, auditTimeout)
		err = c.sendMessage(writeCtx, NewOutputMessage(buf[:n]))
		cancel()
		if err != nil {
			decision := classifyTerminalError(ctx, err, terminalErrorSourceWebSocketWrite, nil, c.isClosing())
			if decision.suppress {
				return
			}
			signal(decision.event)
			return
		}

		c.updateLastActive()
	}
}

// readFromWebSocket reads input from the WebSocket and writes to the PTY.
func (c *Connection) readFromWebSocket(ctx context.Context, auditSvc *audit.Service, signal func(runEvent)) {
	for {
		_, data, err := c.Conn.Read(ctx)
		if err != nil {
			decision := classifyTerminalError(ctx, err, terminalErrorSourceWebSocketRead, nil, c.isClosing())
			if decision.suppress {
				return
			}
			signal(decision.event)
			return
		}

		var msg Message
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}

		c.updateLastActive()

		switch msg.Type {
		case MessageTypeInput:
			input, err := msg.DecodeData()
			if err != nil {
				continue
			}
			if len(input) == 0 {
				continue
			}

			if _, err := c.ptmx.Write(input); err != nil {
				if c.isClosing() || errors.Is(ctx.Err(), context.Canceled) {
					return
				}
				signal(runEvent{
					reason:    terminalEndReasonPTYError,
					sendError: "Failed to write to shell: " + err.Error(),
					err:       err,
				})
				return
			}

			if auditSvc != nil {
				c.accumulateAndLogCommand(auditSvc, input)
			}

		case MessageTypeResize:
			if msg.Cols > 0 && msg.Cols <= 500 && msg.Rows > 0 && msg.Rows <= 500 {
				if err := pty.Setsize(c.ptmx, &pty.Winsize{
					Rows: uint16(msg.Rows), //nolint:gosec // bounds checked above
					Cols: uint16(msg.Cols), //nolint:gosec // bounds checked above
				}); err != nil && !c.isClosing() {
					signal(runEvent{
						reason:    terminalEndReasonPTYError,
						sendError: "Failed to resize terminal: " + err.Error(),
						err:       err,
					})
					return
				}
			}

		case MessageTypeClose:
			signal(runEvent{reason: terminalEndReasonClientClose})
			return

		case MessageTypeOutput, MessageTypeError:
			// These are server-to-client message types, ignore if received from client.
		}
	}
}

// ForceKill expedites terminal teardown during hard server shutdown.
func (c *Connection) ForceKill() {
	c.interruptTransport(false)
	_ = forceKillProcess(c.cmd)
}

func (c *Connection) cleanup(cancel context.CancelFunc, ioWG *sync.WaitGroup, processDone <-chan struct{}, gracefulClose bool) error {
	if !c.closing.CompareAndSwap(false, true) {
		return nil
	}

	cancel()
	c.interruptTransport(gracefulClose)

	ioWG.Wait()

	if c.ptmx != nil {
		_ = c.ptmx.Close()
	}

	return c.terminateProcess(processDone)
}

func (c *Connection) terminateProcess(processDone <-chan struct{}) error {
	if c.cmd == nil {
		return nil
	}
	if waitForSignal(processDone, 0) {
		return nil
	}

	var errs []error
	if err := requestHangup(c.cmd); err != nil {
		errs = append(errs, err)
	}
	if waitForSignal(processDone, processShutdownGrace) {
		return errors.Join(errs...)
	}

	if err := forceKillProcess(c.cmd); err != nil {
		errs = append(errs, err)
	}
	<-processDone
	return errors.Join(errs...)
}

func classifyWebSocketEvent(ctx context.Context, err error, op websocketOp) runEvent {
	source := terminalErrorSourceWebSocketRead
	if op == websocketOpWrite {
		source = terminalErrorSourceWebSocketWrite
	}
	return classifyTerminalError(ctx, err, source, nil, false).event
}

func classifyProcessExit(err error) runEvent {
	if err == nil {
		return runEvent{
			reason:        terminalEndReasonShellExit,
			sendOutput:    "\r\nShell closed.\r\n",
			gracefulClose: true,
		}
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return runEvent{
			reason:        terminalEndReasonShellExit,
			sendError:     "Shell closed: " + err.Error(),
			gracefulClose: true,
		}
	}

	return runEvent{
		reason:    terminalEndReasonPTYError,
		sendError: "Shell closed: " + err.Error(),
		err:       err,
	}
}

func (c *Connection) shouldSuppressPTYReadError(ctx context.Context, err error, processDone <-chan struct{}) bool {
	return classifyTerminalError(ctx, err, terminalErrorSourcePTYRead, processDone, c.isClosing()).suppress
}

func isExpectedShutdownReadError(err error) bool {
	if errors.Is(err, os.ErrClosed) || errors.Is(err, os.ErrDeadlineExceeded) || errors.Is(err, io.EOF) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func classifyTerminalError(ctx context.Context, err error, source terminalErrorSource, processDone <-chan struct{}, closing bool) terminalErrorDecision {
	switch source {
	case terminalErrorSourcePTYRead:
		if errors.Is(ctx.Err(), context.Canceled) && isExpectedShutdownReadError(err) {
			return terminalErrorDecision{suppress: true}
		}
		if closing && isExpectedShutdownReadError(err) {
			return terminalErrorDecision{suppress: true}
		}
		if errors.Is(err, io.EOF) || errors.Is(err, syscall.EIO) {
			return terminalErrorDecision{suppress: waitForSignal(processDone, processExitDetectionDelay)}
		}
		if closing {
			return terminalErrorDecision{suppress: true}
		}
		return terminalErrorDecision{
			event: runEvent{
				reason:    terminalEndReasonPTYError,
				sendError: "Shell closed: " + err.Error(),
				err:       err,
			},
		}
	case terminalErrorSourceWebSocketWrite:
		if closing {
			return terminalErrorDecision{suppress: true}
		}
		fallthrough
	case terminalErrorSourceWebSocketRead:
		if errors.Is(ctx.Err(), context.Canceled) {
			return terminalErrorDecision{
				event: runEvent{reason: terminalEndReasonShutdown},
			}
		}

		status := websocket.CloseStatus(err)
		if status == websocket.StatusNormalClosure || status == websocket.StatusGoingAway {
			return terminalErrorDecision{
				event: runEvent{reason: terminalEndReasonClientClose},
			}
		}
		if status == -1 {
			if source == terminalErrorSourceWebSocketWrite && !errors.Is(err, net.ErrClosed) && !errors.Is(err, io.EOF) {
				return terminalErrorDecision{
					event: runEvent{
						reason: terminalEndReasonDisconnect,
						err:    err,
					},
				}
			}
			return terminalErrorDecision{
				event: runEvent{reason: terminalEndReasonDisconnect},
			}
		}
		return terminalErrorDecision{
			event: runEvent{reason: terminalEndReasonDisconnect},
		}
	default:
		return terminalErrorDecision{}
	}
}

func (c *Connection) interruptTransport(gracefulClose bool) {
	if c.ptmx != nil {
		_ = c.ptmx.SetReadDeadline(time.Now())
	}
	if c.Conn == nil {
		return
	}
	if gracefulClose {
		_ = c.Conn.Close(websocket.StatusNormalClosure, "connection closed")
		return
	}
	_ = c.Conn.CloseNow()
}

func waitForSignal(ch <-chan struct{}, timeout time.Duration) bool {
	if timeout <= 0 {
		select {
		case <-ch:
			return true
		default:
			return false
		}
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-ch:
		return true
	case <-timer.C:
		return false
	}
}

// sendMessage sends a message to the WebSocket.
func (c *Connection) sendMessage(ctx context.Context, msg *Message) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()

	if c.isClosing() {
		return net.ErrClosed
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	return c.Conn.Write(ctx, websocket.MessageText, data)
}

func (c *Connection) sendOutput(output string) {
	ctx, cancel := context.WithTimeout(context.Background(), auditTimeout)
	defer cancel()
	_ = c.sendMessage(ctx, NewOutputMessage([]byte(output)))
}

// sendError sends an error message to the WebSocket.
func (c *Connection) sendError(errMsg string) {
	ctx, cancel := context.WithTimeout(context.Background(), auditTimeout)
	defer cancel()
	_ = c.sendMessage(ctx, NewErrorMessage(errMsg))
}

// updateLastActive updates the last active timestamp.
func (c *Connection) updateLastActive() {
	c.state.Lock()
	defer c.state.Unlock()
	c.LastActive = time.Now()
}

func (c *Connection) isClosing() bool {
	return c.closing.Load()
}

func (c *Connection) logConnectionStart(auditSvc *audit.Service) {
	if auditSvc == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), auditTimeout)
	defer cancel()
	_ = auditSvc.LogTerminalConnectionStart(ctx, c.User.ID, c.User.Username, c.ID, c.IPAddress)
}

func (c *Connection) logConnectionEnd(auditSvc *audit.Service, reason terminalEndReason) {
	if auditSvc == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), auditTimeout)
	defer cancel()
	_ = auditSvc.LogTerminalConnectionEnd(ctx, c.User.ID, c.User.Username, c.ID, reason.String(), c.IPAddress)
}

func (c *Connection) logCommand(auditSvc *audit.Service, command string) {
	if auditSvc == nil || command == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), auditTimeout)
	defer cancel()
	_ = auditSvc.LogTerminalCommand(ctx, c.User.ID, c.User.Username, c.ID, command, c.IPAddress)
}

// accumulateAndLogCommand accumulates input and logs complete commands when Enter is pressed.
func (c *Connection) accumulateAndLogCommand(auditSvc *audit.Service, input []byte) {
	for _, b := range input {
		// Handle ANSI escape sequences (e.g., arrow keys send ESC[A, ESC[B, etc.)
		if c.inEscSeq {
			// Escape sequences end with a letter (A-Z, a-z)
			if (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') {
				c.inEscSeq = false
			}
			continue
		}

		switch b {
		case 27: // ESC - start of escape sequence
			c.inEscSeq = true
		case '\r', '\n':
			// Enter pressed - log the accumulated command
			if len(c.inputBuffer) > 0 {
				c.logCommand(auditSvc, string(c.inputBuffer))
				c.inputBuffer = nil
			}
		case 127, '\b':
			// Backspace - remove last character from buffer
			if len(c.inputBuffer) > 0 {
				c.inputBuffer = c.inputBuffer[:len(c.inputBuffer)-1]
			}
		case 3:
			// Ctrl+C - clear the buffer
			c.inputBuffer = nil
		case 21:
			// Ctrl+U - clear the line
			c.inputBuffer = nil
		default:
			// Regular character - add to buffer
			if b >= 32 && b < 127 {
				c.inputBuffer = append(c.inputBuffer, b)
			}
		}
	}
}

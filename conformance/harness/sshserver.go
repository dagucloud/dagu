// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package harness

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"os/exec"
	"strconv"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// SSHServer is an SSH server in the test process, so conformance tests can
// connect over SSH on every platform without Docker. It runs each command
// with a POSIX shell and forwards connections, so a second server can act as
// a bastion.
type SSHServer struct {
	// Port is the loopback port the server listens on.
	Port string
	// HostKey is the key the server presents.
	HostKey ssh.PublicKey

	listener net.Listener
	config   *ssh.ServerConfig
	shell    string
	wg       sync.WaitGroup

	afterAccept func()

	mu           sync.Mutex
	closed       bool
	conns        map[net.Conn]struct{}
	authAttempts int
	commands     []string
}

// SSHServerOptions sets who may log in.
type SSHServerOptions struct {
	User string
	// Password, when set, is accepted for User.
	Password string
	// AuthorizedKeys are the public keys accepted for User.
	AuthorizedKeys []ssh.PublicKey

	// afterAccept, when set, runs between accepting a connection and
	// registering it, so a test can stop the server in that window.
	afterAccept func()
}

// StartSSHServer starts a server on a loopback port and stops it when the
// test ends. It skips the test when the host has no POSIX shell to run
// commands with.
func StartSSHServer(t *testing.T, opts SSHServerOptions) *SSHServer {
	t.Helper()

	shell := posixShell()
	if shell == "" {
		t.Skip("no POSIX shell (sh or bash) to run SSH commands with")
	}
	_, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating host key: %v", err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostPriv)
	if err != nil {
		t.Fatalf("creating host signer: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening for SSH: %v", err)
	}

	s := &SSHServer{
		Port:     strconv.Itoa(listener.Addr().(*net.TCPAddr).Port),
		HostKey:  hostSigner.PublicKey(),
		listener: listener,
		shell:    shell,
		conns:    make(map[net.Conn]struct{}),

		afterAccept: opts.afterAccept,
	}
	s.config = &ssh.ServerConfig{
		PasswordCallback: func(meta ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			if opts.Password != "" && meta.User() == opts.User && string(password) == opts.Password {
				return nil, nil
			}
			return nil, errors.New("password rejected")
		},
		PublicKeyCallback: func(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if meta.User() == opts.User {
				for _, authorized := range opts.AuthorizedKeys {
					if string(authorized.Marshal()) == string(key.Marshal()) {
						return nil, nil
					}
				}
			}
			return nil, errors.New("public key rejected")
		},
		AuthLogCallback: func(_ ssh.ConnMetadata, method string, _ error) {
			if method != "none" {
				s.mu.Lock()
				s.authAttempts++
				s.mu.Unlock()
			}
		},
	}
	s.config.AddHostKey(hostSigner)

	s.wg.Go(s.serve)
	t.Cleanup(s.close)
	return s
}

// close stops the server. Open connections are closed too, so a client that
// never finishes its handshake cannot hold the test's cleanup.
func (s *SSHServer) close() {
	_ = s.listener.Close()
	s.mu.Lock()
	s.closed = true
	for conn := range s.conns {
		_ = conn.Close()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

// AuthAttempts returns how many times a client offered a credential.
func (s *SSHServer) AuthAttempts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.authAttempts
}

// Commands returns the commands clients asked the server to run, in order.
func (s *SSHServer) Commands() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.commands...)
}

// NewSSHKey returns a new ed25519 key: the private key in OpenSSH PEM form,
// and its public key.
func NewSSHKey(t *testing.T) ([]byte, ssh.PublicKey) {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatalf("marshaling key: %v", err)
	}
	public, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("creating public key: %v", err)
	}
	return pem.EncodeToMemory(block), public
}

func posixShell() string {
	for _, name := range []string{"sh", "bash"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	return ""
}

func (s *SSHServer) serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		if s.afterAccept != nil {
			s.afterAccept()
		}
		// A connection accepted while the server was closing is closed here,
		// since close has already closed the ones it knew about.
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			_ = conn.Close()
			continue
		}
		s.conns[conn] = struct{}{}
		s.mu.Unlock()
		s.wg.Go(func() { s.handle(conn) })
	}
}

func (s *SSHServer) handle(conn net.Conn) {
	defer func() {
		_ = conn.Close()
		s.mu.Lock()
		delete(s.conns, conn)
		s.mu.Unlock()
	}()
	server, channels, requests, err := ssh.NewServerConn(conn, s.config)
	if err != nil {
		return
	}
	defer func() { _ = server.Close() }()
	go ssh.DiscardRequests(requests)

	var wg sync.WaitGroup
	defer wg.Wait()
	for newChannel := range channels {
		switch newChannel.ChannelType() {
		case "session":
			wg.Go(func() { s.session(newChannel) })
		case "direct-tcpip":
			wg.Go(func() { forward(newChannel) })
		default:
			_ = newChannel.Reject(ssh.UnknownChannelType, "unsupported channel type")
		}
	}
}

// session runs one exec or shell request. An empty command runs the shell,
// which reads its script from stdin as the Dagu SSH executor sends it.
func (s *SSHServer) session(newChannel ssh.NewChannel) {
	channel, requests, err := newChannel.Accept()
	if err != nil {
		return
	}
	defer func() { _ = channel.Close() }()

	for req := range requests {
		switch req.Type {
		case "exec", "shell":
			var command string
			if req.Type == "exec" {
				var payload struct{ Command string }
				if err := ssh.Unmarshal(req.Payload, &payload); err != nil {
					_ = req.Reply(false, nil)
					continue
				}
				command = payload.Command
			}
			_ = req.Reply(true, nil)
			s.run(channel, requests, command)
			return
		default:
			// Environment and terminal requests are accepted and ignored.
			if req.WantReply {
				_ = req.Reply(req.Type == "env" || req.Type == "pty-req", nil)
			}
		}
	}
}

// run runs command and sends its exit status. Later requests on the channel
// are declined. They end when the client closes the channel or the
// connection drops, and the command is killed then, so a command cannot
// outlive its client and hold the server's cleanup.
func (s *SSHServer) run(channel ssh.Channel, requests <-chan *ssh.Request, command string) {
	s.mu.Lock()
	s.commands = append(s.commands, command)
	s.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		ssh.DiscardRequests(requests)
		cancel()
	}()

	args := []string{}
	if command != "" {
		args = append(args, "-c", command)
	}
	cmd := exec.CommandContext(ctx, s.shell, args...) //nolint:gosec // The test chooses the command.
	// A background process that keeps the output open after the shell exits
	// or is killed cannot hold Wait for longer than this.
	cmd.WaitDelay = 5 * time.Second
	cmd.Stdout = channel
	cmd.Stderr = channel.Stderr()
	// Stdin is copied apart from the command, so a client that never closes
	// stdin still gets the exit status once the command exits. Closing the
	// channel afterwards ends the copy.
	stdin, err := cmd.StdinPipe()
	if err == nil {
		err = cmd.Start()
	}
	if err == nil {
		go func() {
			_, _ = io.Copy(stdin, channel)
			_ = stdin.Close()
		}()
		err = cmd.Wait()
	}

	status := uint32(0)
	if err != nil {
		status = 255
		// ExitCode is -1 for a process ended by a signal, which keeps 255.
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok && exitErr.ExitCode() >= 0 {
			status = uint32(exitErr.ExitCode()) //nolint:gosec // Non-negative exit codes fit.
		}
	}
	_, _ = channel.SendRequest("exit-status", false, binary.BigEndian.AppendUint32(nil, status))
}

// forward serves a direct-tcpip channel, as a bastion does for a client that
// tunnels to the host behind it.
func forward(newChannel ssh.NewChannel) {
	var target struct {
		Host       string
		Port       uint32
		OriginHost string
		OriginPort uint32
	}
	if err := ssh.Unmarshal(newChannel.ExtraData(), &target); err != nil {
		_ = newChannel.Reject(ssh.ConnectionFailed, "bad direct-tcpip request")
		return
	}
	upstream, err := net.Dial("tcp", net.JoinHostPort(target.Host, strconv.FormatUint(uint64(target.Port), 10)))
	if err != nil {
		_ = newChannel.Reject(ssh.ConnectionFailed, err.Error())
		return
	}
	channel, requests, err := newChannel.Accept()
	if err != nil {
		_ = upstream.Close()
		return
	}
	go ssh.DiscardRequests(requests)

	var wg sync.WaitGroup
	wg.Go(func() {
		_, _ = io.Copy(upstream, channel)
		_ = upstream.(*net.TCPConn).CloseWrite()
	})
	wg.Go(func() {
		_, _ = io.Copy(channel, upstream)
		_ = channel.CloseWrite()
	})
	wg.Wait()
	_ = channel.Close()
	_ = upstream.Close()
}

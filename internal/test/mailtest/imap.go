// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mailtest

import (
	"bytes"
	"cmp"
	"crypto/tls"
	"io"
	"log"
	"net"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

// IMAP is an in-process IMAP server with one user.
type IMAP struct {
	Host     string
	Port     string
	Username string
	Password string

	user       *imapmemserver.User
	startTLS   bool
	mu         sync.Mutex
	specialUse map[string]imap.MailboxAttr
}

// IMAPOption configures StartIMAP.
type IMAPOption func(*imapConfig)

type imapConfig struct {
	startTLS bool
	caps     imap.CapSet
}

// WithSTARTTLS serves plain connections that upgrade with STARTTLS.
func WithSTARTTLS() IMAPOption {
	return func(c *imapConfig) { c.startTLS = true }
}

// WithoutMove advertises neither MOVE nor UIDPLUS.
func WithoutMove() IMAPOption {
	return func(c *imapConfig) { c.caps = imap.CapSet{imap.CapIMAP4rev1: {}} }
}

// StartIMAP starts a server with an empty INBOX. It stops when the test ends.
func StartIMAP(t testing.TB, options ...IMAPOption) *IMAP {
	t.Helper()
	cfg := imapConfig{caps: imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapMove: {}, imap.CapUIDPlus: {}}}
	for _, option := range options {
		option(&cfg)
	}

	s := &IMAP{
		Username:   "user@example.com",
		Password:   "password",
		startTLS:   cfg.startTLS,
		specialUse: map[string]imap.MailboxAttr{},
	}
	s.user = imapmemserver.NewUser(s.Username, s.Password)
	if err := s.user.Create("INBOX", nil); err != nil {
		t.Fatalf("create INBOX: %v", err)
	}

	tlsConfig := ServerTLSConfig(t)
	serverOptions := &imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return &specialUseSession{UserSession: imapmemserver.NewUserSession(s.user), server: s}, nil, nil
		},
		Caps:   cfg.caps,
		Logger: log.New(io.Discard, "", 0),
	}
	var listener net.Listener
	var err error
	if cfg.startTLS {
		serverOptions.TLSConfig = tlsConfig
		listener, err = net.Listen("tcp", "127.0.0.1:0")
	} else {
		listener, err = tls.Listen("tcp", "127.0.0.1:0", tlsConfig)
	}
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := imapserver.New(serverOptions)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })

	s.Host, s.Port = splitAddr(t, listener.Addr())
	return s
}

// CreateFolder creates a folder, optionally marked with a special-use
// attribute such as imap.MailboxAttrArchive.
func (s *IMAP) CreateFolder(t testing.TB, name string, specialUse ...imap.MailboxAttr) {
	t.Helper()
	if err := s.user.Create(name, nil); err != nil {
		t.Fatalf("create folder %q: %v", name, err)
	}
	if len(specialUse) > 0 {
		s.mu.Lock()
		s.specialUse[name] = specialUse[0]
		s.mu.Unlock()
	}
}

// Append stores a raw RFC 5322 message in folder and returns its UID.
func (s *IMAP) Append(t testing.TB, folder, raw string, flags ...imap.Flag) imap.UID {
	t.Helper()
	return s.AppendAt(t, folder, raw, time.Now(), flags...)
}

// AppendAt is Append with the time the server received the message.
func (s *IMAP) AppendAt(t testing.TB, folder, raw string, received time.Time, flags ...imap.Flag) imap.UID {
	t.Helper()
	literal := bytes.NewReader([]byte(raw))
	data, err := s.user.Append(folder, sizedReader{Reader: literal, size: literal.Size()}, &imap.AppendOptions{
		Flags: flags,
		Time:  received,
	})
	if err != nil {
		t.Fatalf("append to %q: %v", folder, err)
	}
	return data.UID
}

// Flags returns the flags of the email with uid in folder, or nil when it is
// not there.
func (s *IMAP) Flags(t testing.TB, folder string, uid imap.UID) []imap.Flag {
	t.Helper()
	var flags []imap.Flag
	s.inspect(t, folder, func(buf *imapclient.FetchMessageBuffer) {
		if buf.UID == uid {
			flags = buf.Flags
		}
	})
	return flags
}

// Subjects lists the subjects in folder, oldest first.
func (s *IMAP) Subjects(t testing.TB, folder string) []string {
	t.Helper()
	subjects := []string{}
	s.inspect(t, folder, func(buf *imapclient.FetchMessageBuffer) {
		subjects = append(subjects, buf.Envelope.Subject)
	})
	return subjects
}

// HasFlag reports whether the email with uid in folder carries flag.
func (s *IMAP) HasFlag(t testing.TB, folder string, uid imap.UID, flag imap.Flag) bool {
	t.Helper()
	return slices.Contains(s.Flags(t, folder, uid), flag)
}

func (s *IMAP) inspect(t testing.TB, folder string, visit func(*imapclient.FetchMessageBuffer)) {
	t.Helper()
	address := net.JoinHostPort(s.Host, s.Port)
	options := &imapclient.Options{
		TLSConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // self-signed test certificate
	}
	dial := imapclient.DialTLS
	if s.startTLS {
		dial = imapclient.DialStartTLS
	}
	client, err := dial(address, options)
	if err != nil {
		t.Fatalf("inspect %q: %v", folder, err)
	}
	defer func() { _ = client.Close() }()
	if err := client.Login(s.Username, s.Password).Wait(); err != nil {
		t.Fatalf("inspect login: %v", err)
	}
	selected, err := client.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		t.Fatalf("inspect select %q: %v", folder, err)
	}
	if selected.NumMessages == 0 {
		return
	}
	var all imap.SeqSet
	all.AddRange(1, 0)
	buffers, err := client.Fetch(all, &imap.FetchOptions{UID: true, Flags: true, Envelope: true}).Collect()
	if err != nil {
		t.Fatalf("inspect fetch %q: %v", folder, err)
	}
	slices.SortFunc(buffers, func(a, b *imapclient.FetchMessageBuffer) int { return cmp.Compare(a.UID, b.UID) })
	for _, buf := range buffers {
		visit(buf)
	}
}

// specialUseSession reports special-use attributes in LIST, which the
// in-memory backend cannot store.
type specialUseSession struct {
	*imapmemserver.UserSession
	server *IMAP
}

func (s *specialUseSession) List(w *imapserver.ListWriter, _ string, _ []string, _ *imap.ListOptions) error {
	s.server.mu.Lock()
	defer s.server.mu.Unlock()
	for name, attr := range s.server.specialUse {
		if err := w.WriteList(&imap.ListData{Mailbox: name, Delim: '/', Attrs: []imap.MailboxAttr{attr}}); err != nil {
			return err
		}
	}
	return nil
}

type sizedReader struct {
	io.Reader
	size int64
}

func (r sizedReader) Size() int64 { return r.size }

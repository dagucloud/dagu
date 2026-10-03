// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mailbox

import (
	"bufio"
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/dagucloud/dagu/v2/internal/test/mailtest"
)

// startStallingServer accepts one IMAP session that answers every command
// except stalled, which it leaves waiting forever.
func startStallingServer(t *testing.T, stalled string) (host, port string) {
	t.Helper()
	return startScriptedServer(t, map[string]string{stalled: ""})
}

// startScriptedServer accepts one IMAP session. A command named in replies
// gets that tagged status reply, or none when it is empty; any other command
// succeeds.
func startScriptedServer(t *testing.T, replies map[string]string) (host, port string) {
	t.Helper()
	listener, err := tls.Listen("tcp", "127.0.0.1:0", mailtest.ServerTLSConfig(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		reader := bufio.NewReader(conn)
		_, _ = conn.Write([]byte("* OK [CAPABILITY IMAP4rev1 MOVE UIDPLUS] ready\r\n"))
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			tag, command := fields[0], strings.ToUpper(fields[1])
			if reply, scripted := replies[command]; scripted {
				if reply != "" {
					_, _ = conn.Write([]byte(tag + " " + reply + "\r\n"))
				}
				continue
			}
			if command == "CAPABILITY" {
				_, _ = conn.Write([]byte("* CAPABILITY IMAP4rev1 MOVE UIDPLUS\r\n"))
			}
			_, _ = conn.Write([]byte(tag + " OK done\r\n"))
		}
	}()
	host, port, err = net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)
	return host, port
}

func dialScripted(t *testing.T, replies map[string]string) *Client {
	t.Helper()
	host, port := startScriptedServer(t, replies)
	client, err := Dial(context.Background(), Account{
		Server:   Server{Host: host, Port: port, Security: SecurityTLS, SkipTLSVerify: true},
		Username: "user",
		Password: "password",
	})
	require.NoError(t, err)
	client.logoutTimeout = 100 * time.Millisecond
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// Only a folder the server says does not exist makes its emails gone; any
// other refusal is an error that keeps the server's reason.
func TestFolderRefusals(t *testing.T) {
	t.Parallel()

	id := emailRef{folder: "Shared", uidValidity: 1, uid: 1}.id()
	missingFolder := map[string]string{
		"EXAMINE": "NO [NONEXISTENT] Unknown mailbox",
		"SELECT":  "NO [NONEXISTENT] Unknown mailbox",
	}
	denied := map[string]string{
		"EXAMINE": "NO [NOPERM] Permission denied",
		"SELECT":  "NO [NOPERM] Permission denied",
	}

	_, err := dialScripted(t, missingFolder).ReplyInfo(id)
	require.ErrorIs(t, err, ErrEmailGone)

	result, err := dialScripted(t, missingFolder).Organize(OrganizeOptions{Items: []Item{{ID: id}}, Mark: MarkRead})
	require.NoError(t, err)
	assert.Equal(t, []string{id}, result.Missing)

	_, err = dialScripted(t, denied).ReplyInfo(id)
	require.ErrorContains(t, err, "Permission denied")
	assert.NotErrorIs(t, err, ErrEmailGone)

	_, err = dialScripted(t, denied).Organize(OrganizeOptions{Items: []Item{{ID: id}}, Mark: MarkRead})
	require.ErrorContains(t, err, "Permission denied")
}

func TestCloseDoesNotWaitForeverForLogout(t *testing.T) {
	t.Parallel()

	host, port := startStallingServer(t, "LOGOUT")
	client, err := Dial(context.Background(), Account{
		Server:   Server{Host: host, Port: port, Security: SecurityTLS, SkipTLSVerify: true},
		Username: "user",
		Password: "password",
	})
	require.NoError(t, err)
	client.logoutTimeout = 100 * time.Millisecond

	closed := make(chan struct{})
	go func() {
		_ = client.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close waited for a LOGOUT reply that never came")
	}
}

// A server that stops answering fails the operation instead of holding the
// step until someone kills it.
func TestSearchFailsWhenServerStopsAnswering(t *testing.T) {
	t.Parallel()

	host, port := startStallingServer(t, "EXAMINE")
	client, err := dial(context.Background(), Account{
		Server:   Server{Host: host, Port: port, Security: SecurityTLS, SkipTLSVerify: true},
		Username: "user",
		Password: "password",
	}, 200*time.Millisecond)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	failed := make(chan error, 1)
	go func() {
		_, _, err := client.Search(SearchOptions{Limit: 1})
		failed <- err
	}()
	select {
	case err := <-failed:
		require.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Search waited forever for a server that stopped answering")
	}
}

// A Gmail API response bounds the time between bytes, not the whole transfer:
// a server that stops sending fails the request, while one that keeps sending
// slowly finishes it.
func TestGmailIdleBound(t *testing.T) {
	t.Parallel()

	const idle = 500 * time.Millisecond
	search := func(t *testing.T, handler http.HandlerFunc) error {
		t.Helper()
		server := httptest.NewServer(handler)
		t.Cleanup(server.Close)
		client, err := dialGmail(context.Background(), Account{
			GmailEndpoint: server.URL + "/",
			Token: func(context.Context) (*oauth2.Token, error) {
				return &oauth2.Token{AccessToken: "token"}, nil
			},
		}, idle)
		require.NoError(t, err)
		t.Cleanup(func() { _ = client.Close() })

		done := make(chan error, 1)
		go func() {
			_, _, err := client.Search(SearchOptions{Limit: 1})
			done <- err
		}()
		select {
		case err := <-done:
			return err
		case <-time.After(10 * time.Second):
			t.Fatal("Search did not finish")
			return nil
		}
	}

	t.Run("Stalled", func(t *testing.T) {
		t.Parallel()
		err := search(t, func(_ http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		})
		require.Error(t, err)
	})

	t.Run("SlowButSteady", func(t *testing.T) {
		t.Parallel()
		err := search(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			flusher, _ := w.(http.Flusher)
			// Leading whitespace is valid JSON; the whole response takes twice
			// the idle bound, a fifth of it between writes.
			for range 10 {
				_, _ = w.Write([]byte(" "))
				flusher.Flush()
				time.Sleep(idle / 5)
			}
			_, _ = w.Write([]byte(`{"messages": []}`))
		})
		require.NoError(t, err)
	})
}

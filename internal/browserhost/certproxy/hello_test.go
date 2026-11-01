// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package certproxy

import (
	"bytes"
	"crypto/tls"
	"io"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// flight returns the first hello a client configured as client sends, and
// the answer of a server configured as server, as each wrote them.
func flight(t *testing.T, client, server *tls.Config) (hello, hello) {
	t.Helper()
	clientEnd, clientWire := net.Pipe()
	serverWire, serverEnd := net.Pipe()
	t.Cleanup(func() {
		for _, conn := range []net.Conn{clientEnd, clientWire, serverWire, serverEnd} {
			_ = conn.Close()
		}
	})
	go func() { _ = tls.Client(clientEnd, client).Handshake() }()
	go func() { _ = tls.Server(serverEnd, server).Handshake() }()
	clientHello, err := readHello(clientWire, handshakeTypeClientHello)
	require.NoError(t, err)
	_, err = serverWire.Write(clientHello.records)
	require.NoError(t, err)
	serverHello, err := readHello(serverWire, handshakeTypeServerHello)
	require.NoError(t, err)
	return clientHello, serverHello
}

// serverConfig returns a server configuration with a certificate for
// siteHost.
func serverConfig(t *testing.T) *tls.Config {
	t.Helper()
	cert, key := newTestCA(t, "hello CA").issue(t, certOptions{name: siteHost, dnsNames: []string{siteHost}})
	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{cert.Raw}, PrivateKey: key}}}
}

// handshakeMessage returns a handshake message of kind holding body.
func handshakeMessage(kind uint8, body []byte) []byte {
	n := len(body)
	return append([]byte{kind, byte(n >> 16), byte(n >> 8), byte(n)}, body...)
}

// records returns payload in records of recordType, each at most size
// bytes long.
func records(recordType uint8, payload []byte, size int) []byte {
	var out []byte
	for len(payload) > 0 {
		n := min(size, len(payload))
		out = append(out, recordType, 3, 3, byte(n>>8), byte(n))
		out = append(out, payload[:n]...)
		payload = payload[n:]
	}
	return out
}

func TestReadHelloTellsTLS13(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		configure func(*tls.Config)
		tls13     bool
	}{
		"TLS13": {tls13: true},
		"TLS12": {configure: func(c *tls.Config) { c.MaxVersion = tls.VersionTLS12 }},
		// A server without the client's key share answers with a
		// HelloRetryRequest, which selects TLS 1.3 as a ServerHello does.
		"HelloRetryRequest": {configure: func(c *tls.Config) { c.CurvePreferences = []tls.CurveID{tls.CurveP384} }, tls13: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			server := serverConfig(t)
			if tc.configure != nil {
				tc.configure(server)
			}
			clientHello, serverHello := flight(t, &tls.Config{ServerName: siteHost, InsecureSkipVerify: true}, server) //nolint:gosec // the handshake never completes
			assert.NotEmpty(t, clientHello.message)
			assert.Equal(t, tc.tls13, selectsTLS13(serverHello.message))
		})
	}
}

// A hello reads the same whether its message spans records, shares one
// with the message after it, or follows an alert; the records come back as
// they were read.
func TestReadHelloAcrossRecords(t *testing.T) {
	t.Parallel()

	_, serverHello := flight(t, &tls.Config{ServerName: siteHost, InsecureSkipVerify: true}, serverConfig(t)) //nolint:gosec // the handshake never completes
	message := handshakeMessage(handshakeTypeServerHello, serverHello.message)
	warning := []byte{recordTypeAlert, 3, 3, 0, 2, 1, 112}
	for name, stream := range map[string][]byte{
		"fragmented":  records(recordTypeHandshake, message, 7),
		"coalesced":   records(recordTypeHandshake, append(bytes.Clone(message), handshakeMessage(11, []byte("certificate"))...), maxRecordLen),
		"after alert": append(bytes.Clone(warning), records(recordTypeHandshake, message, maxRecordLen)...),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := readHello(bytes.NewReader(append(bytes.Clone(stream), "rest"...)), handshakeTypeServerHello)
			require.NoError(t, err)
			assert.Equal(t, serverHello.message, got.message)
			assert.Equal(t, stream, got.records)
			assert.True(t, selectsTLS13(got.message))
		})
	}
}

// Bytes that are not the expected hello fail, returning what was read.
func TestReadHelloRefusesOtherBytes(t *testing.T) {
	t.Parallel()

	clientHello, _ := flight(t, &tls.Config{ServerName: siteHost, InsecureSkipVerify: true}, serverConfig(t)) //nolint:gosec // the handshake never completes
	length := maxRecordLen + 1
	oversized := []byte{recordTypeHandshake, 3, 3, byte(length >> 8), byte(length)}
	// A ServerHello whose header claims 64 KiB.
	tooLong := records(recordTypeHandshake, []byte{handshakeTypeServerHello, 1, 0, 0}, maxRecordLen)
	for name, tc := range map[string]struct {
		stream []byte
		err    error
	}{
		"truncated":       {stream: clientHello.records[:len(clientHello.records)-1], err: io.ErrUnexpectedEOF},
		"another message": {stream: clientHello.records, err: errNotHello},
		"application data": {
			stream: records(23, []byte("data"), maxRecordLen),
			err:    errNotHello,
		},
		"oversized record":  {stream: oversized, err: errNotHello},
		"oversized message": {stream: tooLong, err: errNotHello},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := readHello(bytes.NewReader(tc.stream), handshakeTypeServerHello)
			require.ErrorIs(t, err, tc.err)
			assert.True(t, bytes.HasPrefix(tc.stream, got.records), "the bytes read come back")
		})
	}
}

// A damaged ServerHello never counts as TLS 1.3.
func TestSelectsTLS13RefusesDamagedHello(t *testing.T) {
	t.Parallel()

	_, serverHello := flight(t, &tls.Config{ServerName: siteHost, InsecureSkipVerify: true}, serverConfig(t)) //nolint:gosec // the handshake never completes
	require.True(t, selectsTLS13(serverHello.message))
	for cut := range len(serverHello.message) {
		assert.False(t, selectsTLS13(serverHello.message[:cut]), "cut at %d", cut)
	}
	assert.False(t, selectsTLS13(append(bytes.Clone(serverHello.message), 0)), "trailing byte")
}

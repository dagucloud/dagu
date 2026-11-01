// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package certproxy

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/procutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain runs the test binary as the cert-proxy command when Start
// executes it with the command's arguments.
func TestMain(m *testing.M) {
	if len(os.Args) == 3 && os.Args[1] == "browser" && os.Args[2] == "cert-proxy" {
		if err := Main(context.Background(), os.Stdin, os.Stdout); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func wireInput(t *testing.T, version int, cfg Config) io.Reader {
	t.Helper()
	data, err := json.Marshal(wireConfig{Version: version, Config: cfg})
	require.NoError(t, err)
	return strings.NewReader(string(data))
}

func TestMainWritesOneReadyLine(t *testing.T) {
	t.Parallel()

	ca := newTestCA(t, "corp CA")
	cfg := testConfig(t, "", ca.clientEntry(t, testLabel, siteHost))
	stdout, stdoutWriter := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Main(ctx, wireInput(t, configVersion, cfg), stdoutWriter) }()

	reader := bufio.NewReader(stdout)
	line, err := reader.ReadBytes('\n')
	require.NoError(t, err)
	var ready Ready
	require.NoError(t, json.Unmarshal(line, &ready))
	assert.Positive(t, ready.Port)
	assert.NotEmpty(t, ready.SPKI)
	assert.True(t, strings.HasPrefix(ready.PACURL, pacDataURLPrefix))
	_, err = reader.ReadByte()
	assert.ErrorIs(t, err, io.EOF, "stdout is closed after the ready line")

	events, err := Events(context.Background(), controlURL(ready.Port, cfg.Token), 0)
	require.NoError(t, err)
	assert.Empty(t, events)

	cancel()
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Main did not return after its context ended")
	}
}

func TestMainReportsBadConfig(t *testing.T) {
	t.Parallel()

	ca := newTestCA(t, "corp CA")
	valid := testConfig(t, "", ca.clientEntry(t, testLabel, siteHost))
	noEntries, noBrowser := valid, valid
	noEntries.Entries = nil
	noBrowser.CDPURL = ""
	for name, input := range map[string]io.Reader{
		"Malformed":      strings.NewReader("{"),
		"UnknownVersion": wireInput(t, configVersion+1, valid),
		"NoEntries":      wireInput(t, configVersion, noEntries),
		"NoBrowser":      wireInput(t, configVersion, noBrowser),
		"MissingVersion": wireInput(t, 0, valid),
	} {
		stdout, stdoutWriter := io.Pipe()
		done := make(chan error, 1)
		go func() { done <- Main(context.Background(), input, stdoutWriter) }()
		output, err := io.ReadAll(stdout)
		require.NoError(t, err, name)
		var line readyLine
		require.NoError(t, json.Unmarshal(output, &line), name)
		assert.NotEmpty(t, line.Error, name)
		assert.Error(t, <-done, name)
	}
}

func TestStart(t *testing.T) {
	t.Parallel()

	executable, err := os.Executable()
	require.NoError(t, err)
	ca := newTestCA(t, "corp CA")
	cfg := testConfig(t, "", ca.clientEntry(t, testLabel, siteHost))

	handle, err := Start(context.Background(), executable, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = handle.Kill() })
	assert.Positive(t, handle.PID)
	assert.True(t, strings.HasSuffix(handle.URL, "/"+cfg.Token))
	assert.Equal(t, []string{
		"--proxy-pac-url=" + handle.PACURL,
		"--ignore-certificate-errors-spki-list=" + handle.SPKI,
	}, handle.ChromeArgs())
	_, err = Events(context.Background(), handle.URL, 0)
	require.NoError(t, err)

	require.NoError(t, handle.Kill())
	require.Eventually(t, func() bool {
		return !procutil.IsAlive(handle.PID)
	}, 5*time.Second, 50*time.Millisecond, "the proxy runs after Kill")
	assert.NoError(t, handle.Kill(), "a proxy already gone is not an error")
}

func TestStartedProxyExitsWithBrowser(t *testing.T) {
	t.Parallel()

	executable, err := os.Executable()
	require.NoError(t, err)
	ca := newTestCA(t, "corp CA")
	browser := newFakeBrowser(t)
	cfg := Config{Token: rand.Text(), CDPURL: browser.server.URL, Entries: []Entry{ca.clientEntry(t, testLabel, siteHost)}}

	handle, err := Start(context.Background(), executable, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = handle.Kill() })
	_, err = Events(context.Background(), handle.URL, 0)
	require.NoError(t, err)
	// A browser that closes before the proxy first saw it is one that has
	// not started yet, which the proxy waits for.
	require.Eventually(t, func() bool { return browser.answered.Load() > 0 },
		3*browserPollInterval+5*time.Second, 50*time.Millisecond, "the proxy never checked on its browser")

	browser.server.Close()
	require.Eventually(t, func() bool {
		return !procutil.IsAlive(handle.PID)
	}, 3*browserPollInterval+5*time.Second, 100*time.Millisecond, "the proxy outlived its browser")
}

func TestStartReportsFailure(t *testing.T) {
	t.Parallel()

	executable, err := os.Executable()
	require.NoError(t, err)
	ca := newTestCA(t, "corp CA")
	entry := ca.clientEntry(t, testLabel, siteHost)
	entry.KeyPEM = "not a key"
	cfg := testConfig(t, "", entry)

	_, err = Start(context.Background(), executable, cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the certificate proxy did not start: "+testLabel)
}

// A proxy that took the browser's debugging port, on its certificate port
// or its guard's, is one Start replaces.
func TestReadyListensOn(t *testing.T) {
	t.Parallel()

	assert.True(t, Ready{Port: 9333}.listensOn(9333))
	assert.True(t, Ready{Port: 9100, GuardPort: 9333}.listensOn(9333))
	assert.False(t, Ready{Port: 9100, GuardPort: 9101}.listensOn(9333))
	assert.False(t, Ready{Port: 9100}.listensOn(0), "a proxy without a guard has no guard port to collide")
}

// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sysproxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	pacFetchTimeout = 10 * time.Second
	maxPACSize      = 1 << 20
)

// pacClient fetches proxy auto-config scripts. It connects directly, since
// the script being fetched is what chooses the proxy.
var pacClient = &http.Client{
	Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true},
	Timeout:   pacFetchTimeout,
}

// fetchPAC returns the proxy auto-config script at an http, https or file
// URL, unchanged.
func fetchPAC(ctx context.Context, rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	var script []byte
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		script, err = readHTTP(ctx, u.String())
	case "file":
		script, err = readFile(u)
	default:
		return "", fmt.Errorf("scheme %q is not supported", u.Scheme)
	}
	if err != nil {
		return "", err
	}
	// Captive portals and DNS hijacking can answer with a web page instead.
	if !bytes.Contains(script, []byte("FindProxyForURL")) {
		return "", errors.New("the response is not a proxy auto-config script")
	}
	return string(script), nil
}

func readHTTP(ctx context.Context, scriptURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, scriptURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := pacClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP status %s", resp.Status)
	}
	return readLimited(resp.Body)
}

func readFile(u *url.URL) ([]byte, error) {
	if u.Host != "" && u.Host != "localhost" {
		return nil, fmt.Errorf("file URL host %q is not this computer", u.Host)
	}
	path := u.Path
	// file:///C:/proxy.pac names C:/proxy.pac on Windows.
	if runtime.GOOS == "windows" && len(path) > 2 && path[0] == '/' && path[2] == ':' {
		path = path[1:]
	}
	file, err := os.Open(filepath.Clean(filepath.FromSlash(path)))
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return readLimited(file)
}

func readLimited(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxPACSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxPACSize {
		return nil, fmt.Errorf("the script is larger than %d bytes", maxPACSize)
	}
	return data, nil
}

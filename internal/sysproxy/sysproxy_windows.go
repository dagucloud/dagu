// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sysproxy

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// autoDetectTimeout bounds auto-discovery, which can block for a long time
// on networks that do not answer.
const autoDetectTimeout = 5 * time.Second

// Arguments and errors of WinHTTP auto-discovery.
const (
	winhttpAutoDetectTypeDHCP = 0x1
	winhttpAutoDetectTypeDNSA = 0x2
	// errWinHTTPAutodetectionFailed reports that the network offers no
	// proxy auto-config.
	errWinHTTPAutodetectionFailed windows.Errno = 12180
)

var (
	modwinhttp  = windows.NewLazySystemDLL("winhttp.dll")
	modkernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procWinHTTPGetIEProxyConfigForCurrentUser = modwinhttp.NewProc("WinHttpGetIEProxyConfigForCurrentUser")
	procWinHTTPDetectAutoProxyConfigURL       = modwinhttp.NewProc("WinHttpDetectAutoProxyConfigUrl")
	procGlobalFree                            = modkernel32.NewProc("GlobalFree")
)

// Sources of the Windows proxy settings. Tests replace them.
var (
	readIEProxyConfig   = currentUserIEProxyConfig
	detectAutoConfigURL = winHTTPDetectAutoConfigURL
)

// winhttpCurrentUserIEProxyConfig is WINHTTP_CURRENT_USER_IE_PROXY_CONFIG.
type winhttpCurrentUserIEProxyConfig struct {
	autoDetect    int32
	autoConfigURL *uint16
	proxy         *uint16
	proxyBypass   *uint16
}

// detectSystem follows the current user's Internet Options proxy settings,
// which Chrome on Windows follows as well.
func detectSystem(ctx context.Context) (string, []string) {
	ie, err := readIEProxyConfig()
	if err != nil {
		return directPAC, []string{fmt.Sprintf("could not read the Windows proxy settings: %v; requests go DIRECT", err)}
	}
	cfg, warnings := ie.systemConfig()
	pac, more := cfg.pac(ctx, discoverAutoConfigURL)
	return pac, append(warnings, more...)
}

func currentUserIEProxyConfig() (ieProxyConfig, error) {
	var raw winhttpCurrentUserIEProxyConfig
	ok, _, err := procWinHTTPGetIEProxyConfigForCurrentUser.Call(uintptr(unsafe.Pointer(&raw))) //nolint:gosec // WinHTTP fills the structure at this address
	if ok == 0 {
		// A user who never set Internet Options has no settings to read.
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
			return ieProxyConfig{}, nil
		}
		return ieProxyConfig{}, err
	}
	defer globalFree(raw.autoConfigURL)
	defer globalFree(raw.proxy)
	defer globalFree(raw.proxyBypass)
	return ieProxyConfig{
		autoDetect:    raw.autoDetect != 0,
		autoConfigURL: windows.UTF16PtrToString(raw.autoConfigURL),
		proxy:         windows.UTF16PtrToString(raw.proxy),
		proxyBypass:   windows.UTF16PtrToString(raw.proxyBypass),
	}, nil
}

// discoverAutoConfigURL asks the network for its proxy auto-config URL, and
// gives up after autoDetectTimeout.
func discoverAutoConfigURL(ctx context.Context) (string, error) {
	type result struct {
		url string
		err error
	}
	detect := detectAutoConfigURL
	done := make(chan result, 1)
	go func() {
		scriptURL, err := detect()
		done <- result{url: scriptURL, err: err}
	}()
	timer := time.NewTimer(autoDetectTimeout)
	defer timer.Stop()
	select {
	case r := <-done:
		return r.url, r.err
	case <-timer.C:
		return "", fmt.Errorf("the network did not answer within %s", autoDetectTimeout)
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func winHTTPDetectAutoConfigURL() (string, error) {
	var scriptURL *uint16
	ok, _, err := procWinHTTPDetectAutoProxyConfigURL.Call(
		winhttpAutoDetectTypeDHCP|winhttpAutoDetectTypeDNSA,
		uintptr(unsafe.Pointer(&scriptURL)), //nolint:gosec // WinHTTP stores the URL pointer at this address
	)
	if ok == 0 {
		if errors.Is(err, errWinHTTPAutodetectionFailed) {
			return "", errors.New("the network offers no proxy auto-config")
		}
		return "", err
	}
	defer globalFree(scriptURL)
	return windows.UTF16PtrToString(scriptURL), nil
}

// globalFree releases a string WinHTTP allocated.
func globalFree(s *uint16) {
	if s != nil {
		_, _, _ = procGlobalFree.Call(uintptr(unsafe.Pointer(s))) //nolint:gosec // GlobalFree takes the allocation's address
	}
}

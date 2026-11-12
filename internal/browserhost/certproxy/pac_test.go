// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package certproxy

import (
	"context"
	"encoding/base64"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/dop251/goja"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testProxyPort = 4567

// testNow is a Wednesday afternoon.
var testNow = time.Date(2026, time.October, 14, 15, 30, 45, 0, time.UTC)

// testPACEnv resolves a few names and reads a fixed clock.
func testPACEnv() pacEnv {
	names := map[string][]netip.Addr{
		"portal.corp": {netip.MustParseAddr("10.1.2.3")},
		"v6.corp":     {netip.MustParseAddr("fd00::1")},
		"dual.corp":   {netip.MustParseAddr("fd00::2"), netip.MustParseAddr("10.0.0.2")},
	}
	return pacEnv{
		lookup: func(_ context.Context, host string) ([]netip.Addr, error) {
			if addrs, ok := names[host]; ok {
				return append([]netip.Addr(nil), addrs...), nil
			}
			return nil, errors.New("no such host")
		},
		localAddrs: func() []netip.Addr {
			return []netip.Addr{netip.MustParseAddr("fd00::10"), netip.MustParseAddr("192.168.1.10")}
		},
		now:     func() time.Time { return testNow },
		timeout: 200 * time.Millisecond,
	}
}

func newTestEvaluator(t *testing.T, script string) *pacEvaluator {
	t.Helper()
	evaluator, err := newPACEvaluator(script, testPACEnv())
	require.NoError(t, err)
	return evaluator
}

func findProxy(t *testing.T, evaluator *pacEvaluator, rawURL string) string {
	t.Helper()
	host := strings.TrimPrefix(strings.TrimPrefix(rawURL, "https://"), "http://")
	host, _, _ = strings.Cut(host, "/")
	result, err := evaluator.evaluate(context.Background(), rawURL, host)
	require.NoError(t, err)
	return result
}

func TestComposePAC(t *testing.T) {
	t.Parallel()

	hosts := []string{"portal.example.com", "*.corp.example", "[FE80::1]", "10.0.0.5"}
	const proxy = "PROXY 127.0.0.1:4567"
	system := `function FindProxyForURL(url, host) { return "PROXY system:8080"; }`
	evaluator := newTestEvaluator(t, composePAC(testProxyPort, 0, hosts, system))

	for rawURL, want := range map[string]string{
		"https://portal.example.com/":  proxy,
		"https://PORTAL.example.com./": proxy,
		"https://a.b.corp.example/":    proxy,
		"https://fe80::1/":             proxy,
		"https://10.0.0.5/":            proxy,
		"http://portal.example.com/":   "PROXY system:8080",
		"https://corp.example/":        "PROXY system:8080",
		"https://other.example.com/":   "PROXY system:8080",
		"https://10.0.0.6/":            "PROXY system:8080",
	} {
		assert.Equal(t, want, findProxy(t, evaluator, rawURL), rawURL)
	}
}

// With a guard, the other https: URLs go to it first, falling back to the
// system's route; http: URLs keep the system's route.
func TestComposePACGuard(t *testing.T) {
	t.Parallel()

	hosts := []string{"portal.example.com"}
	system := `function FindProxyForURL(url, host) { return "PROXY system:8080; DIRECT"; }`
	evaluator := newTestEvaluator(t, composePAC(testProxyPort, 4568, hosts, system))

	for rawURL, want := range map[string]string{
		"https://portal.example.com/": "PROXY 127.0.0.1:4567",
		"https://other.example.com/":  "PROXY 127.0.0.1:4568; PROXY system:8080; DIRECT",
		"https://10.0.0.6/":           "PROXY 127.0.0.1:4568; PROXY system:8080; DIRECT",
		"http://other.example.com/":   "PROXY system:8080; DIRECT",
	} {
		assert.Equal(t, want, findProxy(t, evaluator, rawURL), rawURL)
	}
}

func TestComposePACWrapsSystemScript(t *testing.T) {
	t.Parallel()

	hosts := []string{"portal.example.com"}
	const proxy = "PROXY 127.0.0.1:4567"
	for name, tt := range map[string]struct {
		system    string
		wantOther string
	}{
		"Empty": {system: "", wantOther: "DIRECT"},
		"ThrowsWhenCalled": {
			system:    `function FindProxyForURL(url, host) { throw new Error("broken"); }`,
			wantOther: "DIRECT",
		},
		"ThrowsWhenLoaded": {
			system:    `function FindProxyForURL(url, host) { return "PROXY unreached:1"; } throw "broken";`,
			wantOther: "DIRECT",
		},
		"OnlyEx": {
			system:    `function FindProxyForURLEx(url, host) { return "PROXY ex:1"; }`,
			wantOther: "PROXY ex:1",
		},
		"AssignsGlobalFunction": {
			system:    `function init() { FindProxyForURL = function (url, host) { return "PROXY global:1"; }; } init();`,
			wantOther: "PROXY global:1",
		},
		"AssignsGlobalEx": {
			system:    `(function () { FindProxyForURLEx = function (url, host) { return "PROXY leaked:1"; }; })();`,
			wantOther: "PROXY leaked:1",
		},
		"UsesHelpers": {
			system:    `function FindProxyForURL(url, host) { return isInNet(dnsResolve("portal.corp"), "10.0.0.0", "255.0.0.0") ? "PROXY helpers:1" : "DIRECT"; }`,
			wantOther: "PROXY helpers:1",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			evaluator := newTestEvaluator(t, composePAC(testProxyPort, 0, hosts, tt.system))
			assert.Equal(t, proxy, findProxy(t, evaluator, "https://portal.example.com/"))
			assert.Equal(t, tt.wantOther, findProxy(t, evaluator, "https://other.example.com/"))
		})
	}
}

func TestComposePACAnyHost(t *testing.T) {
	t.Parallel()

	evaluator := newTestEvaluator(t, composePAC(testProxyPort, 0, []string{"*"}, ""))
	assert.Equal(t, "PROXY 127.0.0.1:4567", findProxy(t, evaluator, "https://anything.example/"))
	assert.Equal(t, "DIRECT", findProxy(t, evaluator, "http://anything.example/"))
}

func TestPACLocation(t *testing.T) {
	t.Parallel()

	const served = "http://127.0.0.1:1/token/proxy.pac"
	// The longest script whose data: URL fits the limit.
	size := (maxPACDataURL - len(pacDataURLPrefix)) / 4 * 3
	script := strings.Repeat("x", size)

	location, isServed := pacLocation(script, served)
	assert.False(t, isServed)
	assert.LessOrEqual(t, len(location), maxPACDataURL)
	require.True(t, strings.HasPrefix(location, pacDataURLPrefix))
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(location, pacDataURLPrefix))
	require.NoError(t, err)
	assert.Equal(t, script, string(decoded))

	location, isServed = pacLocation(script+"x", served)
	assert.True(t, isServed)
	assert.Equal(t, served, location)
}

// evalHelper runs a JavaScript expression with the PAC helpers.
func evalHelper(t *testing.T, evaluator *pacEvaluator, expr string) string {
	t.Helper()
	value, err := evaluator.guard(context.Background(), func() (goja.Value, error) {
		return evaluator.vm.RunString("String(" + expr + ")")
	})
	require.NoError(t, err, expr)
	return value.String()
}

func TestPACHelpers(t *testing.T) {
	t.Parallel()

	evaluator := newTestEvaluator(t, `function FindProxyForURL(url, host) { return "DIRECT"; }`)
	for expr, want := range map[string]string{
		`isPlainHostName("intranet")`:                                  "true",
		`isPlainHostName("www.corp.example")`:                          "false",
		`dnsDomainIs("www.corp.example", ".corp.example")`:             "true",
		`dnsDomainIs("www.other.example", ".corp.example")`:            "false",
		`localHostOrDomainIs("www", "www.corp.example")`:               "true",
		`localHostOrDomainIs("www.corp.example", "www.corp.example")`:  "true",
		`localHostOrDomainIs("www.other.example", "www.corp.example")`: "false",
		`isResolvable("portal.corp")`:                                  "true",
		`isResolvable("missing.corp")`:                                 "false",
		`dnsResolve("portal.corp")`:                                    "10.1.2.3",
		`dnsResolve("dual.corp")`:                                      "10.0.0.2",
		`dnsResolve("v6.corp")`:                                        "null",
		`dnsResolve("missing.corp")`:                                   "null",
		`isInNet("portal.corp", "10.0.0.0", "255.0.0.0")`:              "true",
		`isInNet("10.1.2.3", "10.1.0.0", "255.255.0.0")`:               "true",
		`isInNet("192.168.0.1", "10.0.0.0", "255.0.0.0")`:              "false",
		`isInNet("missing.corp", "10.0.0.0", "255.0.0.0")`:             "false",
		`convert_addr("10.1.2.3")`:                                     "167838211",
		`convert_addr("192.168.0.1")`:                                  "-1062731775",
		`myIpAddress()`:                                                "192.168.1.10",
		`dnsDomainLevels("www.corp.example")`:                          "2",
		`shExpMatch("http://www.corp.example/x", "*.corp.example/*")`:  "true",
		`shExpMatch("a.example", "?.example")`:                         "true",
		`shExpMatch("ab.example", "?.example")`:                        "false",
		`shExpMatch("aXexample", "a.example")`:                         "false",
		`isResolvableEx("v6.corp")`:                                    "true",
		`isResolvableEx("missing.corp")`:                               "false",
		`dnsResolveEx("dual.corp")`:                                    "fd00::2;10.0.0.2",
		`dnsResolveEx("missing.corp")`:                                 "",
		`myIpAddressEx()`:                                              "fd00::10;192.168.1.10",
		`isInNetEx("10.1.2.3", "10.0.0.0/8")`:                          "true",
		`isInNetEx("fd00::1", "fd00::/8")`:                             "true",
		`isInNetEx("10.1.2.3", "192.168.0.0/16")`:                      "false",
		`sortIpAddressList("10.0.0.2;fd00::1;10.0.0.1")`:               "fd00::1;10.0.0.1;10.0.0.2",
		`sortIpAddressList("x;10.0.0.1")`:                              "false",
		`getClientVersion()`:                                           "1.0",
	} {
		assert.Equal(t, want, evalHelper(t, evaluator, expr), expr)
	}
}

func TestPACTimeHelpers(t *testing.T) {
	t.Parallel()

	// testNow is Wednesday 2026-10-14 15:30:45 UTC.
	evaluator := newTestEvaluator(t, `function FindProxyForURL(url, host) { return "DIRECT"; }`)
	for expr, want := range map[string]bool{
		`weekdayRange("WED")`:                        true,
		`weekdayRange("MON", "FRI")`:                 true,
		`weekdayRange("SAT", "SUN")`:                 false,
		`weekdayRange("FRI", "TUE")`:                 false,
		`weekdayRange("TUE", "MON")`:                 true,
		`weekdayRange("WED", "GMT")`:                 true,
		`weekdayRange("XYZ")`:                        false,
		`dateRange("OCT")`:                           true,
		`dateRange(14)`:                              true,
		`dateRange(2026)`:                            true,
		`dateRange("SEP", "NOV")`:                    true,
		`dateRange("NOV", "FEB")`:                    false,
		`dateRange("DEC", "OCT")`:                    true,
		`dateRange(1, 15)`:                           true,
		`dateRange(15, 31)`:                          false,
		`dateRange(14, "OCT", 14, "OCT")`:            true,
		`dateRange(1, "OCT", 2026, 31, "DEC", 2026)`: true,
		`dateRange("JAN", 2027, "MAR", 2027)`:        false,
		`dateRange("OCT", "GMT")`:                    true,
		`timeRange(15)`:                              true,
		`timeRange(9, 17)`:                           true,
		`timeRange(16, 18)`:                          false,
		`timeRange(22, 2)`:                           false,
		`timeRange(23, 16)`:                          true,
		`timeRange(15, 30, 15, 30)`:                  true,
		`timeRange(15, 30, 0, 15, 30, 44)`:           false,
		`timeRange(15, 30, 0, 15, 30, 45)`:           true,
		`timeRange(15, "GMT")`:                       true,
	} {
		assert.Equal(t, want, evalHelper(t, evaluator, expr) == "true", expr)
	}
}

func TestPACEvaluator(t *testing.T) {
	t.Parallel()

	t.Run("PrefersEx", func(t *testing.T) {
		t.Parallel()
		evaluator := newTestEvaluator(t, `
function FindProxyForURL(url, host) { return "PROXY plain:1"; }
function FindProxyForURLEx(url, host) { return "PROXY ex:1"; }`)
		assert.Equal(t, "PROXY ex:1", findProxy(t, evaluator, "https://portal.example.com/"))
	})

	t.Run("EmptyRoutesDirectly", func(t *testing.T) {
		t.Parallel()
		evaluator := newTestEvaluator(t, " \n")
		result := evaluator.routes(context.Background(), "https://portal.example.com/", "portal.example.com")
		assert.Equal(t, []route{{kind: routeDirect}}, result.routes)
	})

	t.Run("RejectsInvalid", func(t *testing.T) {
		t.Parallel()
		for name, script := range map[string]string{
			"Syntax":     `function FindProxyForURL(url, host) {`,
			"NoFunction": `var x = 1;`,
			"Loops":      `while (true) {}`,
		} {
			_, err := newPACEvaluator(script, testPACEnv())
			assert.Error(t, err, name)
		}
	})

	t.Run("InterruptsLoop", func(t *testing.T) {
		t.Parallel()
		evaluator := newTestEvaluator(t, `function FindProxyForURL(url, host) { while (true) {} }`)
		began := time.Now()
		_, err := evaluator.evaluate(context.Background(), "https://portal.example.com/", "portal.example.com")
		require.Error(t, err)
		assert.Less(t, time.Since(began), 2*time.Second)
		// A script that does not answer routes directly, and later calls
		// still run.
		result := evaluator.routes(context.Background(), "https://portal.example.com/", "portal.example.com")
		assert.Equal(t, []route{{kind: routeDirect}}, result.routes)
	})

	t.Run("CachesPerURL", func(t *testing.T) {
		t.Parallel()
		evaluator := newTestEvaluator(t, `var calls = 0; function FindProxyForURL(url, host) { calls++; return "PROXY p" + calls + ":1"; }`)
		first := evaluator.routes(context.Background(), "https://portal.example.com/", "portal.example.com")
		again := evaluator.routes(context.Background(), "https://portal.example.com/", "portal.example.com")
		other := evaluator.routes(context.Background(), "https://other.example.com/", "other.example.com")
		assert.Equal(t, []route{{kind: routeProxy, addr: "p1:1"}}, first.routes)
		assert.Equal(t, first, again)
		assert.Equal(t, []route{{kind: routeProxy, addr: "p2:1"}}, other.routes)
	})
}

func TestParsePACResult(t *testing.T) {
	t.Parallel()

	for value, want := range map[string]pacResult{
		"": {routes: []route{{kind: routeDirect}}},
		"PROXY a:8080; SOCKS b:1080; HTTPS c; SOCKS5 d:1081; DIRECT": {
			routes: []route{
				{kind: routeProxy, addr: "a:8080"},
				{kind: routeSOCKS4, addr: "b:1080"},
				{kind: routeHTTPS, addr: "c:443"},
				{kind: routeSOCKS5, addr: "d:1081"},
				{kind: routeDirect},
			},
		},
		"proxy a ; http [::1]": {routes: []route{{kind: routeProxy, addr: "a:80"}, {kind: routeProxy, addr: "[::1]:80"}}},
		"SOCKS4 x:1":           {routes: []route{{kind: routeSOCKS4, addr: "x:1"}}},
		"QUIC x:1":             {skipped: []string{"QUIC x:1"}},
		"PROXY":                {skipped: []string{"PROXY"}},
		"SOCKS5 s":             {routes: []route{{kind: routeSOCKS5, addr: "s:1080"}}},
	} {
		assert.Equal(t, want, parsePACResult(value), value)
	}
}

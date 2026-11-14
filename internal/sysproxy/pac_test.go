// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sysproxy

import (
	"net"
	"net/url"
	"strings"
	"testing"

	"github.com/dop251/goja"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pacHelpers are the standard PAC functions the generated scripts call,
// defined the way browsers define them.
const pacHelpers = `
function isPlainHostName(host) { return host.indexOf(".") < 0; }
function dnsDomainIs(host, domain) {
  return host.length >= domain.length && host.substring(host.length - domain.length) == domain;
}
function shExpMatch(str, shexp) {
  var re = shexp.replace(/[.+^${}()|[\]\\]/g, "\\$&").replace(/\*/g, ".*").replace(/\?/g, ".");
  return new RegExp("^" + re + "$").test(str);
}
function isInNet(host, pattern, mask) {
  var addr = dnsResolve(host);
  if (!addr) return false;
  return (ipNumber(addr) & ipNumber(mask)) == (ipNumber(pattern) & ipNumber(mask));
}
function ipNumber(ip) {
  var p = ip.split(".");
  return ((+p[0] << 24) | (+p[1] << 16) | (+p[2] << 8) | +p[3]) >>> 0;
}
`

// testResolver stands in for DNS: only these names resolve. A host name
// inside a bypassed range shows whether a script resolves names for isInNet.
var testResolver = map[string]string{"intranet.example": "10.1.2.3"}

// routeCase is a request URL and the PAC result expected for it.
type routeCase struct {
	url  string
	want string
}

// evalPAC runs script's FindProxyForURL for rawURL, passing the host the way
// Chrome does: lower-cased and without IPv6 brackets.
func evalPAC(t *testing.T, script, rawURL string) string {
	t.Helper()
	vm := goja.New()
	require.NoError(t, vm.Set("dnsResolve", func(host string) any {
		if ip := net.ParseIP(host); ip != nil {
			return ip.String()
		}
		if ip, ok := testResolver[host]; ok {
			return ip
		}
		return nil
	}))
	_, err := vm.RunString(pacHelpers)
	require.NoError(t, err)
	_, err = vm.RunString(script)
	require.NoError(t, err, script)
	find, ok := goja.AssertFunction(vm.Get("FindProxyForURL"))
	require.True(t, ok, "the script defines no FindProxyForURL")
	u, err := url.Parse(rawURL)
	require.NoError(t, err)
	result, err := find(goja.Undefined(), vm.ToValue(rawURL), vm.ToValue(strings.ToLower(u.Hostname())))
	require.NoError(t, err)
	return result.String()
}

func assertRoutes(t *testing.T, script string, cases []routeCase) {
	t.Helper()
	for _, c := range cases {
		assert.Equal(t, c.want, evalPAC(t, script, c.url), "%s\n%s", c.url, script)
	}
}

func TestStaticConfigPAC(t *testing.T) {
	t.Parallel()
	httpServer := proxyServer{kind: httpProxy, host: "web.example", port: "3128"}
	secureServer := proxyServer{kind: httpProxy, host: "secure.example", port: "3129"}
	socksServer := proxyServer{kind: socks5Proxy, host: "socks.example", port: "1080"}

	t.Run("SchemeSpecificBeforeFallback", func(t *testing.T) {
		t.Parallel()
		script := staticConfig{
			bypass:   []string{hostCondition("direct.example")},
			http:     []proxyServer{httpServer},
			https:    []proxyServer{secureServer},
			fallback: []proxyServer{socksServer},
		}.pac()
		assertRoutes(t, script, []routeCase{
			{"http://www.example.com/", "PROXY web.example:3128"},
			{"https://www.example.com/", "PROXY secure.example:3129"},
			{"ws://www.example.com/", "SOCKS5 socks.example:1080"},
			{"https://direct.example/", "DIRECT"},
			{"http://localhost:8080/", "DIRECT"},
			{"http://127.0.0.1/", "DIRECT"},
			{"http://[::1]/", "DIRECT"},
		})
	})

	t.Run("FallbackOnly", func(t *testing.T) {
		t.Parallel()
		script := staticConfig{fallback: []proxyServer{httpServer, socksServer}}.pac()
		assertRoutes(t, script, []routeCase{
			{"https://www.example.com/", "PROXY web.example:3128; SOCKS5 socks.example:1080"},
		})
	})

	t.Run("SchemeWithoutProxyGoesDirect", func(t *testing.T) {
		t.Parallel()
		script := staticConfig{https: []proxyServer{secureServer}}.pac()
		assertRoutes(t, script, []routeCase{
			{"https://www.example.com/", "PROXY secure.example:3129"},
			{"http://www.example.com/", "DIRECT"},
		})
	})

	// Chrome sends WebSockets under fixed rules through the fallback
	// proxies, else the https: ones, else the http: ones.
	t.Run("WebSocketPrefersHTTPSProxy", func(t *testing.T) {
		t.Parallel()
		script := staticConfig{http: []proxyServer{httpServer}, https: []proxyServer{secureServer}}.pac()
		assertRoutes(t, script, []routeCase{
			{"ws://www.example.com/socket", "PROXY secure.example:3129"},
			{"wss://www.example.com/", "PROXY secure.example:3129"},
			{"ws://localhost/socket", "DIRECT"},
		})
		script = staticConfig{http: []proxyServer{httpServer}}.pac()
		assertRoutes(t, script, []routeCase{
			{"ws://www.example.com/socket", "PROXY web.example:3128"},
			{"https://www.example.com/", "DIRECT"},
		})
	})

	t.Run("NoProxies", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, directPAC, staticConfig{bypass: []string{plainHostCondition}}.pac())
	})
}

func TestBypassCondition(t *testing.T) {
	t.Parallel()
	entries := []string{"<local>", "*.local", "169.254/16", ".corp.example", "Exact.Example", "10.9.8.7", "fe80::1", "build-??.example"}
	conditions := make([]string, 0, len(entries))
	for _, entry := range entries {
		condition, err := bypassCondition(entry)
		require.NoError(t, err, entry)
		conditions = append(conditions, condition)
	}
	script := staticConfig{bypass: conditions, fallback: []proxyServer{{kind: httpProxy, host: "proxy.example", port: "8080"}}}.pac()
	const proxied = "PROXY proxy.example:8080"
	assertRoutes(t, script, []routeCase{
		{"http://intranet/", "DIRECT"},
		{"http://printer.local/", "DIRECT"},
		{"http://169.254.10.20/", "DIRECT"},
		{"http://169.255.0.1/", proxied},
		{"http://a.corp.example/", "DIRECT"},
		{"http://corp.example/", proxied},
		{"http://exact.example/", "DIRECT"},
		{"http://sub.exact.example/", proxied},
		{"http://10.9.8.7/", "DIRECT"},
		{"http://[fe80::1]/", "DIRECT"},
		{"http://build-01.example/", "DIRECT"},
		{"http://build-001.example/", proxied},
		{"http://www.example.com/", proxied},
	})

	for _, entry := range []string{"fe80::/10", "10.0.0.0/33", "http://example.com", "*.example.com:8080", "bad host"} {
		_, err := bypassCondition(entry)
		assert.Error(t, err, entry)
	}
}

func TestJSStringEscapesScriptSyntax(t *testing.T) {
	t.Parallel()
	hostile := "a\"); return \"DIRECT\"; (\" \\"
	vm := goja.New()
	value, err := vm.RunString(jsString(hostile))
	require.NoError(t, err)
	assert.Equal(t, hostile, value.String())
}

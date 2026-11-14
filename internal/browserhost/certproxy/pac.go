// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package certproxy

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/netip"

	"github.com/dagucloud/dagu/v2/internal/cmn/hostpattern"
)

const (
	// pacDataURLPrefix starts a PAC script carried in its URL.
	pacDataURLPrefix = "data:application/x-ns-proxy-autoconfig;base64,"
	// pacContentType is the media type of a served PAC script.
	pacContentType = "application/x-ns-proxy-autoconfig"
	// maxPACDataURL bounds a PAC data: URL, which travels on the browser's
	// command line; Windows limits a command line to 32 KiB.
	maxPACDataURL = 16 << 10
)

// pacTemplate routes the https: URLs of the certificate hosts to the proxy
// and everything else the way the system PAC script, %[3]s, does. With a
// guard, %[4]s, the other https: URLs go to the guard first; the system's
// route follows it, so the browser falls back to that route once the guard
// stops accepting connections.
//
// The system script is the body of a function, so the functions it
// declares stay out of the global scope, and a script that throws while
// loading routes directly, as it would on its own. The function it ends up
// defining is kept before Dagu's FindProxyForURL replaces any global one it
// assigned. Browsers prefer a global FindProxyForURLEx, so a global one the
// system script assigned is removed. Dagu's function is assigned, not
// declared, so it is not hoisted where the system script would find it.
//
// Browsers pass PAC scripts only the scheme, host, and port of https:
// URLs, and wss: URLs as https:. The host matching follows
// hostpattern.Match: %[1]s is the proxy's PAC result and %[2]s the
// patterns, canonical and as JSON.
const pacTemplate = `var __daguSystem = (function () {
  var load = function () {
%[3]s
;
    return typeof FindProxyForURLEx === "function" ? FindProxyForURLEx :
      typeof FindProxyForURL === "function" ? FindProxyForURL : null;
  };
  var find = null;
  try { find = load(); } catch (e) {}
  return function (url, host) {
    if (!find) return "DIRECT";
    try { return find(url, host); } catch (e) { return "DIRECT"; }
  };
})();
FindProxyForURLEx = undefined;
FindProxyForURL = function (url, host) {
  var proxy = %[1]s, patterns = %[2]s, guard = %[4]s;
  if (String(url).substring(0, 6).toLowerCase() === "https:") {
    var h = String(host).toLowerCase().replace(/\.+$/, "");
    if (h.charAt(0) === "[" && h.charAt(h.length - 1) === "]") h = h.substring(1, h.length - 1);
    for (var i = 0; h !== "" && i < patterns.length; i++) {
      var p = patterns[i];
      if (p === "*" || h === p) return proxy;
      if (p.substring(0, 2) === "*." && h.substring(h.length - p.length + 1) === p.substring(1)) return proxy;
    }
    if (guard !== "" && h !== "") return guard + "; " + __daguSystem(url, host);
  }
  return __daguSystem(url, host);
};
`

// composePAC returns the PAC script the browser uses: the https: URLs of
// hosts go to the proxy on proxyPort, other https: URLs to the guard on
// guardPort, if not zero, and everything else follows the system script.
func composePAC(proxyPort, guardPort int, hosts []string, system string) string {
	patterns := []string{}
	for _, host := range hosts {
		patterns = append(patterns, pacPattern(host))
	}
	proxy, _ := json.Marshal(proxyResult(proxyPort))
	guard, _ := json.Marshal("")
	if guardPort != 0 {
		guard, _ = json.Marshal(proxyResult(guardPort))
	}
	list, _ := json.Marshal(patterns)
	return fmt.Sprintf(pacTemplate, proxy, list, system, guard)
}

// proxyResult is the PAC result that sends a request to the local port.
func proxyResult(port int) string {
	return fmt.Sprintf("PROXY 127.0.0.1:%d", port)
}

// pacPattern returns pattern in the form a browser gives the host it
// matches, IP addresses included.
func pacPattern(pattern string) string {
	pattern = hostpattern.Canonical(pattern)
	if addr, err := netip.ParseAddr(pattern); err == nil {
		return addr.Unmap().String()
	}
	return pattern
}

// pacLocation returns the URL by which the browser loads script: a data:
// URL when it is small enough, or servedURL, which the proxy then serves.
func pacLocation(script, servedURL string) (location string, served bool) {
	location = pacDataURLPrefix + base64.StdEncoding.EncodeToString([]byte(script))
	if len(location) <= maxPACDataURL {
		return location, false
	}
	return servedURL, true
}

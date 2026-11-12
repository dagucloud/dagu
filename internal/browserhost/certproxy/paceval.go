// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package certproxy

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
)

const (
	// pacEvalTimeout bounds loading the PAC script and each call to it,
	// DNS lookups included.
	pacEvalTimeout = 2 * time.Second
	// pacCacheTTL is how long the routes found for a URL are reused.
	pacCacheTTL = time.Minute
	// pacMaxCallStackSize bounds the script's recursion.
	pacMaxCallStackSize = 10_000
	// pacClientVersion is what getClientVersion reports: the version of the
	// IPv6 extensions the evaluator implements.
	pacClientVersion = "1.0"
	// pacGMT ends the arguments of the date and time functions that read
	// the time in UTC.
	pacGMT = "GMT"
	// pacFallbackAddress is what myIpAddress reports without a network.
	pacFallbackAddress = "127.0.0.1"
	// pacListSeparator separates the addresses of the Ex functions.
	pacListSeparator = ";"
)

// localRouteProbes are addresses whose routes tell the device's primary
// addresses. Dialing UDP sends nothing.
var localRouteProbes = []string{"8.8.8.8:53", "[2001:4860:4860::8888]:53"}

// Default proxy ports of the PAC result keywords.
const (
	defaultHTTPProxyPort  = "80"
	defaultHTTPSProxyPort = "443"
	defaultSOCKSPort      = "1080"
)

var errNoFindProxy = errors.New("the proxy script defines neither FindProxyForURL nor FindProxyForURLEx")

// pacEnv is what the PAC helper functions read from the device.
type pacEnv struct {
	lookup     func(ctx context.Context, host string) ([]netip.Addr, error)
	localAddrs func() []netip.Addr
	now        func() time.Time
	timeout    time.Duration
}

func systemPACEnv() pacEnv {
	return pacEnv{
		lookup: func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		},
		localAddrs: localAddresses,
		now:        time.Now,
		timeout:    pacEvalTimeout,
	}
}

// pacEvaluator runs a PAC script to route the proxy's own connections. An
// evaluator without a script routes everything directly.
type pacEvaluator struct {
	env pacEnv
	mu  sync.Mutex
	vm  *goja.Runtime
	// find is the script's FindProxyForURLEx, or else its FindProxyForURL.
	find goja.Callable
	// ctx bounds the call in progress, and the lookups it makes.
	ctx   context.Context
	cache map[string]pacCacheEntry
}

type pacCacheEntry struct {
	result  pacResult
	expires time.Time
}

// pacResult is the routes a PAC script gives a URL, in order of preference.
type pacResult struct {
	routes []route
	// skipped are the script's routes the proxy cannot take, as written.
	skipped []string
}

// newPACEvaluator loads script. An empty script routes everything directly;
// a script that fails to load returns an error.
func newPACEvaluator(script string, env pacEnv) (*pacEvaluator, error) {
	e := &pacEvaluator{env: env, cache: map[string]pacCacheEntry{}}
	if strings.TrimSpace(script) == "" {
		return e, nil
	}
	e.vm = goja.New()
	e.vm.SetMaxCallStackSize(pacMaxCallStackSize)
	for name, fn := range e.helpers() {
		if err := e.vm.Set(name, fn); err != nil {
			return nil, err
		}
	}
	if _, err := e.guard(context.Background(), func() (goja.Value, error) { return e.vm.RunString(script) }); err != nil {
		return nil, err
	}
	for _, name := range []string{"FindProxyForURLEx", "FindProxyForURL"} {
		if find, ok := goja.AssertFunction(e.vm.Get(name)); ok {
			e.find = find
			return e, nil
		}
	}
	return nil, errNoFindProxy
}

// routes returns the routes for rawURL, whose host is host. A script that
// fails routes directly.
func (e *pacEvaluator) routes(ctx context.Context, rawURL, host string) pacResult {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now()
	if cached, ok := e.cache[rawURL]; ok && now.Before(cached.expires) {
		return cached.result
	}
	for key, cached := range e.cache {
		if !now.Before(cached.expires) {
			delete(e.cache, key)
		}
	}
	value, err := e.evaluateLocked(ctx, rawURL, host)
	if err != nil {
		value = ""
	}
	result := parsePACResult(value)
	e.cache[rawURL] = pacCacheEntry{result: result, expires: now.Add(pacCacheTTL)}
	return result
}

// evaluate returns what the script answers for rawURL.
func (e *pacEvaluator) evaluate(ctx context.Context, rawURL, host string) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.evaluateLocked(ctx, rawURL, host)
}

func (e *pacEvaluator) evaluateLocked(ctx context.Context, rawURL, host string) (string, error) {
	if e.find == nil {
		return routeKeywordDirect, nil
	}
	value, err := e.guard(ctx, func() (goja.Value, error) {
		return e.find(goja.Undefined(), e.vm.ToValue(rawURL), e.vm.ToValue(host))
	})
	if err != nil {
		return "", err
	}
	return value.String(), nil
}

// guard runs fn on the script's runtime, interrupting it after the
// evaluation timeout or when ctx ends.
func (e *pacEvaluator) guard(ctx context.Context, fn func() (goja.Value, error)) (goja.Value, error) {
	ctx, cancel := context.WithTimeout(ctx, e.env.timeout)
	defer cancel()
	e.ctx = ctx
	done, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-ctx.Done():
			e.vm.Interrupt(ctx.Err())
		case <-done:
		}
	}()
	value, err := fn()
	close(done)
	<-stopped
	e.vm.ClearInterrupt()
	e.ctx = nil
	return value, err
}

// parsePACResult reads a PAC result such as "PROXY a:8080; SOCKS5 b:1080;
// DIRECT". An empty result means DIRECT.
func parsePACResult(value string) pacResult {
	var result pacResult
	for item := range strings.SplitSeq(value, ";") {
		fields := strings.Fields(item)
		if len(fields) == 0 {
			continue
		}
		keyword := strings.ToUpper(fields[0])
		if keyword == routeKeywordDirect && len(fields) == 1 {
			result.routes = append(result.routes, route{kind: routeDirect})
			continue
		}
		kind, port, ok := proxyKind(keyword)
		if !ok || len(fields) != 2 {
			result.skipped = append(result.skipped, strings.Join(fields, " "))
			continue
		}
		result.routes = append(result.routes, route{kind: kind, addr: withDefaultPort(fields[1], port)})
	}
	if len(result.routes) == 0 && len(result.skipped) == 0 {
		result.routes = []route{{kind: routeDirect}}
	}
	return result
}

// proxyKind returns the route a PAC keyword names and its default port.
// SOCKS and SOCKS4 name SOCKS4, as in Chrome.
func proxyKind(keyword string) (routeKind, string, bool) {
	switch keyword {
	case routeKeywordProxy, "HTTP":
		return routeProxy, defaultHTTPProxyPort, true
	case routeKeywordHTTPS:
		return routeHTTPS, defaultHTTPSProxyPort, true
	case routeKeywordSOCKS5:
		return routeSOCKS5, defaultSOCKSPort, true
	case routeKeywordSOCKS, "SOCKS4":
		return routeSOCKS4, defaultSOCKSPort, true
	default:
		return 0, "", false
	}
}

func withDefaultPort(addr, port string) string {
	if _, _, err := net.SplitHostPort(addr); err == nil {
		return addr
	}
	return net.JoinHostPort(strings.Trim(addr, "[]"), port)
}

// helpers returns the functions PAC scripts call, those of Netscape's
// definition and Microsoft's IPv6 extensions.
func (e *pacEvaluator) helpers() map[string]func(goja.FunctionCall) goja.Value {
	vm := e.vm
	value := func(v any) goja.Value { return vm.ToValue(v) }
	return map[string]func(goja.FunctionCall) goja.Value{
		"isPlainHostName": func(call goja.FunctionCall) goja.Value {
			return value(!strings.Contains(stringArg(call, 0), "."))
		},
		"dnsDomainIs": func(call goja.FunctionCall) goja.Value {
			return value(strings.HasSuffix(strings.ToLower(stringArg(call, 0)), strings.ToLower(stringArg(call, 1))))
		},
		"localHostOrDomainIs": func(call goja.FunctionCall) goja.Value {
			host, domain := strings.ToLower(stringArg(call, 0)), strings.ToLower(stringArg(call, 1))
			return value(host == domain || strings.HasPrefix(domain, host+"."))
		},
		"isResolvable": func(call goja.FunctionCall) goja.Value {
			_, ok := e.resolve4(stringArg(call, 0))
			return value(ok)
		},
		"dnsResolve": func(call goja.FunctionCall) goja.Value {
			if addr, ok := e.resolve4(stringArg(call, 0)); ok {
				return value(addr.String())
			}
			return goja.Null()
		},
		"isInNet": func(call goja.FunctionCall) goja.Value {
			return value(e.isInNet(stringArg(call, 0), stringArg(call, 1), stringArg(call, 2)))
		},
		"convert_addr": func(call goja.FunctionCall) goja.Value {
			addr, ok := ipv4(stringArg(call, 0))
			if !ok {
				return value(0)
			}
			return value(int32(addrBits(addr))) //nolint:gosec // reinterpreting the bits, as JavaScript's bitwise operators do
		},
		"myIpAddress": func(goja.FunctionCall) goja.Value {
			for _, addr := range e.env.localAddrs() {
				if addr.Is4() {
					return value(addr.String())
				}
			}
			return value(pacFallbackAddress)
		},
		"dnsDomainLevels": func(call goja.FunctionCall) goja.Value {
			return value(strings.Count(stringArg(call, 0), "."))
		},
		"shExpMatch": func(call goja.FunctionCall) goja.Value {
			return value(shExpMatch(stringArg(call, 0), stringArg(call, 1)))
		},
		"weekdayRange": func(call goja.FunctionCall) goja.Value {
			now, args := e.clock(call)
			return value(weekdayRange(now, args))
		},
		"dateRange": func(call goja.FunctionCall) goja.Value {
			now, args := e.clock(call)
			return value(dateRange(now, args))
		},
		"timeRange": func(call goja.FunctionCall) goja.Value {
			now, args := e.clock(call)
			return value(timeRange(now, args))
		},
		"isResolvableEx": func(call goja.FunctionCall) goja.Value {
			return value(len(e.resolve(stringArg(call, 0))) > 0)
		},
		"dnsResolveEx": func(call goja.FunctionCall) goja.Value {
			return value(joinAddrs(e.resolve(stringArg(call, 0))))
		},
		"myIpAddressEx": func(goja.FunctionCall) goja.Value {
			return value(joinAddrs(e.env.localAddrs()))
		},
		"isInNetEx": func(call goja.FunctionCall) goja.Value {
			addr, addrErr := netip.ParseAddr(stringArg(call, 0))
			prefix, prefixErr := netip.ParsePrefix(stringArg(call, 1))
			return value(addrErr == nil && prefixErr == nil && prefix.Contains(addr.Unmap()))
		},
		"sortIpAddressList": func(call goja.FunctionCall) goja.Value {
			sorted, ok := sortAddrList(stringArg(call, 0))
			if !ok {
				return value(false)
			}
			return value(sorted)
		},
		"getClientVersion": func(goja.FunctionCall) goja.Value {
			return value(pacClientVersion)
		},
	}
}

// resolve returns the addresses of host, or nothing when it does not
// resolve in time.
func (e *pacEvaluator) resolve(host string) []netip.Addr {
	if addr, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		return []netip.Addr{addr.Unmap()}
	}
	if host == "" || e.ctx == nil {
		return nil
	}
	addrs, err := e.env.lookup(e.ctx, host)
	if err != nil {
		return nil
	}
	for i := range addrs {
		addrs[i] = addrs[i].Unmap()
	}
	return addrs
}

func (e *pacEvaluator) resolve4(host string) (netip.Addr, bool) {
	for _, addr := range e.resolve(host) {
		if addr.Is4() {
			return addr, true
		}
	}
	return netip.Addr{}, false
}

// isInNet reports whether host, or the IPv4 address it resolves to, is in
// the network of pattern and mask.
func (e *pacEvaluator) isInNet(host, pattern, mask string) bool {
	network, ok := ipv4(pattern)
	if !ok {
		return false
	}
	bits, ok := ipv4(mask)
	if !ok {
		return false
	}
	addr, ok := ipv4(host)
	if !ok {
		if addr, ok = e.resolve4(host); !ok {
			return false
		}
	}
	return addrBits(addr)&addrBits(bits) == addrBits(network)&addrBits(bits)
}

// clock returns the time the date and time functions compare, in UTC when
// the arguments end with GMT, and the arguments before it.
func (e *pacEvaluator) clock(call goja.FunctionCall) (time.Time, []string) {
	args := make([]string, len(call.Arguments))
	for i := range call.Arguments {
		args[i] = stringArg(call, i)
	}
	now := e.env.now()
	if n := len(args); n > 0 && strings.EqualFold(args[n-1], pacGMT) {
		return now.UTC(), args[:n-1]
	}
	return now, args
}

func stringArg(call goja.FunctionCall, i int) string {
	arg := call.Argument(i)
	if goja.IsUndefined(arg) || goja.IsNull(arg) {
		return ""
	}
	return strings.TrimSpace(arg.String())
}

func ipv4(text string) (netip.Addr, bool) {
	addr, err := netip.ParseAddr(text)
	return addr, err == nil && addr.Is4()
}

func addrBits(addr netip.Addr) uint32 {
	b := addr.As4()
	return binary.BigEndian.Uint32(b[:])
}

func joinAddrs(addrs []netip.Addr) string {
	texts := make([]string, len(addrs))
	for i, addr := range addrs {
		texts[i] = addr.String()
	}
	return strings.Join(texts, pacListSeparator)
}

// sortAddrList sorts a semicolon-separated list of addresses, IPv6 first.
func sortAddrList(list string) (string, bool) {
	var addrs []netip.Addr
	for item := range strings.SplitSeq(list, pacListSeparator) {
		addr, err := netip.ParseAddr(strings.TrimSpace(item))
		if err != nil {
			return "", false
		}
		addrs = append(addrs, addr)
	}
	slices.SortStableFunc(addrs, func(a, b netip.Addr) int {
		if a.Is6() != b.Is6() {
			if a.Is6() {
				return -1
			}
			return 1
		}
		return a.Compare(b)
	})
	return joinAddrs(addrs), true
}

// shExpMatch matches text against a shell expression, where * matches any
// run of characters and ? any one character. Like browsers, it hands other
// characters to a regular expression.
func shExpMatch(text, pattern string) bool {
	var expr strings.Builder
	expr.WriteString("^")
	for _, r := range pattern {
		switch r {
		case '*':
			expr.WriteString(".*")
		case '?':
			expr.WriteString(".")
		case '.':
			expr.WriteString(`\.`)
		default:
			expr.WriteRune(r)
		}
	}
	expr.WriteString("$")
	re, err := regexp.Compile(expr.String())
	return err == nil && re.MatchString(text)
}

var weekdays = map[string]time.Weekday{
	"SUN": time.Sunday, "MON": time.Monday, "TUE": time.Tuesday, "WED": time.Wednesday,
	"THU": time.Thursday, "FRI": time.Friday, "SAT": time.Saturday,
}

var months = map[string]time.Month{
	"JAN": time.January, "FEB": time.February, "MAR": time.March, "APR": time.April,
	"MAY": time.May, "JUN": time.June, "JUL": time.July, "AUG": time.August,
	"SEP": time.September, "OCT": time.October, "NOV": time.November, "DEC": time.December,
}

// weekdayRange reports whether now falls on the weekday, or in the range of
// weekdays, args names. A range may wrap past Saturday.
func weekdayRange(now time.Time, args []string) bool {
	if len(args) == 0 || len(args) > 2 {
		return false
	}
	first, ok := weekdays[strings.ToUpper(args[0])]
	if !ok {
		return false
	}
	last := first
	if len(args) == 2 {
		if last, ok = weekdays[strings.ToUpper(args[1])]; !ok {
			return false
		}
	}
	return inRange(int(now.Weekday()), int(first), int(last))
}

// dateFields is one end of a date range; zero fields are unset.
type dateFields struct {
	day   int
	month time.Month
	year  int
}

// dateRange reports whether now falls on the day, month, or year args name,
// or between the two dates its halves name, each a day, a month, a year,
// or a combination of them. A range may wrap past the end of the month or
// year.
func dateRange(now time.Time, args []string) bool {
	switch len(args) {
	case 1:
		field, ok := parseDateFields(args)
		switch {
		case !ok:
			return false
		case field.day != 0:
			return now.Day() == field.day
		case field.month != 0:
			return now.Month() == field.month
		default:
			return now.Year() == field.year
		}
	case 2, 4, 6:
	default:
		return false
	}
	half := len(args) / 2
	from, fromOK := parseDateFields(args[:half])
	to, toOK := parseDateFields(args[half:])
	if !fromOK || !toOK {
		return false
	}
	start := from.resolve(now, time.January, 1)
	// The end day is included whole, through the last day of its month
	// when only the month is named.
	end := to.resolve(now, time.December, 0).AddDate(0, 0, 1)
	if !start.After(end) {
		return !now.Before(start) && now.Before(end)
	}
	return !now.Before(start) || now.Before(end)
}

// parseDateFields reads days (numbers below 32), years (larger numbers),
// and month names.
func parseDateFields(args []string) (dateFields, bool) {
	var fields dateFields
	for _, arg := range args {
		if n, err := strconv.Atoi(arg); err == nil {
			if n > 0 && n < 32 {
				fields.day = n
			} else {
				fields.year = n
			}
			continue
		}
		month, ok := months[strings.ToUpper(arg)]
		if !ok {
			return fields, false
		}
		fields.month = month
	}
	return fields, true
}

// resolve returns the start of the day the fields name, in now's year and
// location. A day alone falls in now's month; otherwise an unset month is
// defaultMonth and an unset day defaultDay, where zero means the month's
// last day.
func (f dateFields) resolve(now time.Time, defaultMonth time.Month, defaultDay int) time.Time {
	year, month, day := now.Year(), f.month, f.day
	if f.year != 0 {
		year = f.year
	}
	if month == 0 {
		month = defaultMonth
		if f.day != 0 && f.year == 0 {
			month = now.Month()
		}
	}
	if day == 0 {
		if day = defaultDay; day == 0 {
			day = time.Date(year, month+1, 0, 0, 0, 0, 0, now.Location()).Day()
		}
	}
	return time.Date(year, month, day, 0, 0, 0, 0, now.Location())
}

// timeRange reports whether now falls in the hour, or between the times,
// args name: hours, hours and minutes, or hours, minutes, and seconds. A
// range may wrap past midnight; its end includes the whole hour or minute
// it names.
func timeRange(now time.Time, args []string) bool {
	values := make([]int, len(args))
	for i, arg := range args {
		n, err := strconv.Atoi(arg)
		if err != nil {
			return false
		}
		values[i] = n
	}
	second := now.Hour()*3600 + now.Minute()*60 + now.Second()
	switch len(values) {
	case 1:
		return now.Hour() == values[0]
	case 2:
		return inRange(now.Hour(), values[0], values[1])
	case 4:
		return inRange(second, values[0]*3600+values[1]*60, values[2]*3600+values[3]*60+59)
	case 6:
		return inRange(second, values[0]*3600+values[1]*60+values[2], values[3]*3600+values[4]*60+values[5])
	default:
		return false
	}
}

// inRange reports whether v is in [first, last], wrapping when last is
// below first.
func inRange(v, first, last int) bool {
	if first <= last {
		return first <= v && v <= last
	}
	return v >= first || v <= last
}

// localAddresses returns the device's addresses, those of its routes to the
// internet first, without loopback or link-local addresses.
func localAddresses() []netip.Addr {
	var addrs []netip.Addr
	add := func(addr netip.Addr) {
		addr = addr.Unmap()
		if addr.IsValid() && !addr.IsLoopback() && !addr.IsLinkLocalUnicast() && !slices.Contains(addrs, addr) {
			addrs = append(addrs, addr)
		}
	}
	for _, probe := range localRouteProbes {
		conn, err := net.Dial("udp", probe)
		if err != nil {
			continue
		}
		if local, ok := conn.LocalAddr().(*net.UDPAddr); ok {
			add(local.AddrPort().Addr())
		}
		_ = conn.Close()
	}
	interfaceAddrs, _ := net.InterfaceAddrs()
	for _, interfaceAddr := range interfaceAddrs {
		if prefix, err := netip.ParsePrefix(interfaceAddr.String()); err == nil {
			add(prefix.Addr())
		}
	}
	return addrs
}

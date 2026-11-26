// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package connection

import (
	"net/url"
	"slices"
)

// Preset is one service of a provider that a connection can use, such as
// gmail. Presets are compiled into the binary.
type Preset struct {
	Provider Provider
	Name     string
	// MCPURL is the service's MCP server, or empty when it has none.
	MCPURL    string
	RESTHosts []string
	// ReadScopes are asked for with AccessRead and AccessWrite.
	ReadScopes []string
	// WriteScopes are asked for with AccessWrite, beyond ReadScopes.
	WriteScopes []string
	// APIs are the Google API IDs to enable in the app's project.
	APIs []string
}

const googleScopePrefix = "https://www.googleapis.com/auth/"

// presets follow each product's MCP setup guide. A guide lists every scope
// its tools use; those that allow no change form ReadScopes and the rest
// form WriteScopes.
var presets = []Preset{
	{
		Provider:    ProviderGoogle,
		Name:        "gmail",
		MCPURL:      "https://gmailmcp.googleapis.com/mcp/v1",
		RESTHosts:   []string{"gmail.googleapis.com"},
		ReadScopes:  googleScopes("gmail.readonly"),
		WriteScopes: googleScopes("gmail.compose", "gmail.modify"),
		APIs:        []string{"gmail.googleapis.com", "gmailmcp.googleapis.com"},
	},
	{
		Provider:  ProviderGoogle,
		Name:      "calendar",
		MCPURL:    "https://calendarmcp.googleapis.com/mcp/v1",
		RESTHosts: []string{"www.googleapis.com"},
		ReadScopes: googleScopes(
			"calendar.calendarlist.readonly",
			"calendar.events.freebusy",
			"calendar.events.readonly",
		),
		WriteScopes: googleScopes("calendar.events"),
		APIs:        []string{"calendar-json.googleapis.com", "calendarmcp.googleapis.com"},
	},
	{
		Provider:    ProviderGoogle,
		Name:        "drive",
		MCPURL:      "https://drivemcp.googleapis.com/mcp/v1",
		RESTHosts:   []string{"www.googleapis.com"},
		ReadScopes:  googleScopes("drive.readonly"),
		WriteScopes: googleScopes("drive.file"),
		APIs:        []string{"drive.googleapis.com", "drivemcp.googleapis.com"},
	},
	{
		Provider:    ProviderGoogle,
		Name:        "docs",
		MCPURL:      "https://docsmcp.googleapis.com/mcp/v1",
		RESTHosts:   []string{"docs.googleapis.com"},
		ReadScopes:  googleScopes("drive.readonly", "documents.readonly"),
		WriteScopes: googleScopes("drive.file", "documents"),
		APIs:        []string{"docs.googleapis.com", "docsmcp.googleapis.com"},
	},
	{
		Provider:    ProviderGoogle,
		Name:        "sheets",
		MCPURL:      "https://sheetsmcp.googleapis.com/mcp/v1",
		RESTHosts:   []string{"sheets.googleapis.com"},
		ReadScopes:  googleScopes("drive.readonly", "spreadsheets.readonly"),
		WriteScopes: googleScopes("drive.file", "spreadsheets"),
		APIs:        []string{"sheets.googleapis.com", "sheetsmcp.googleapis.com"},
	},
	{
		Provider:    ProviderGoogle,
		Name:        "slides",
		MCPURL:      "https://slidesmcp.googleapis.com/mcp/v1",
		RESTHosts:   []string{"slides.googleapis.com"},
		ReadScopes:  googleScopes("drive.readonly", "presentations.readonly"),
		WriteScopes: googleScopes("drive.file", "presentations"),
		APIs:        []string{"slides.googleapis.com", "slidesmcp.googleapis.com"},
	},
	{
		Provider:  ProviderGoogle,
		Name:      "chat",
		MCPURL:    "https://chatmcp.googleapis.com/mcp/v1",
		RESTHosts: []string{"chat.googleapis.com"},
		ReadScopes: googleScopes(
			"chat.spaces.readonly",
			"chat.memberships.readonly",
			"chat.messages.readonly",
		),
		WriteScopes: googleScopes(
			"chat.messages.create",
			"chat.users.readstate",
			"chat.users.spacesettings",
		),
		APIs: []string{"chat.googleapis.com", "chatmcp.googleapis.com"},
	},
	{
		Provider:  ProviderGoogle,
		Name:      "people",
		MCPURL:    "https://people.googleapis.com/mcp/v1",
		RESTHosts: []string{"people.googleapis.com"},
		ReadScopes: googleScopes(
			"directory.readonly",
			"userinfo.profile",
			"contacts.readonly",
		),
		APIs: []string{"people.googleapis.com"},
	},
	{
		Provider: ProviderGoogle,
		Name:     "search",
		MCPURL:   "https://workspacemcp.googleapis.com/mcp/v1",
		ReadScopes: googleScopes(
			"gmail.readonly",
			"drive.readonly",
			"calendar.readonly",
			"chat.messages.readonly",
			"chat.spaces.readonly",
			"chat.memberships.readonly",
		),
		APIs: []string{"workspacemcp.googleapis.com"},
	},
}

// providerScopes are asked for by every connection of a provider, whatever
// its services. Google's let Dagu show the account.
var providerScopes = map[Provider][]string{
	ProviderGoogle: {"openid", "email"},
}

// LookupPreset returns the preset of provider named service.
func LookupPreset(provider Provider, service string) (Preset, bool) {
	for _, p := range presets {
		if p.Provider == provider && p.Name == service {
			return p, true
		}
	}
	return Preset{}, false
}

// Scopes returns the scopes p asks for with access.
func (p *Preset) Scopes(access Access) []string {
	if access == AccessWrite {
		return slices.Concat(p.ReadScopes, p.WriteScopes)
	}
	return slices.Clone(p.ReadScopes)
}

// Hosts returns the hosts that may receive a token for p: its MCP URL's
// host and its REST hosts.
func (p *Preset) Hosts() []string {
	var hosts []string
	if host := urlHost(p.MCPURL); host != "" {
		hosts = append(hosts, host)
	}
	return appendUnique(hosts, p.RESTHosts...)
}

// RequestedScopes returns the scopes c asks for: those every connection of
// its provider asks for, those of each service's preset for its access,
// and its Scopes, each once.
func (c *Connection) RequestedScopes() []string {
	scopes := appendUnique(nil, providerScopes[c.Provider]...)
	for _, svc := range c.Services {
		if p, ok := LookupPreset(c.Provider, svc.Name); ok {
			scopes = appendUnique(scopes, p.Scopes(svc.Access)...)
		}
	}
	return appendUnique(scopes, c.Scopes...)
}

// AllBoundHosts returns the hosts that may receive c's access tokens: the
// hosts of its services' presets, its BoundHosts, and the host of its
// Resource, as host:port when the URL names a port, each once.
func (c *Connection) AllBoundHosts() []string {
	var hosts []string
	for _, svc := range c.Services {
		if p, ok := LookupPreset(c.Provider, svc.Name); ok {
			hosts = appendUnique(hosts, p.Hosts()...)
		}
	}
	hosts = appendUnique(hosts, c.BoundHosts...)
	if host := urlHost(c.Resource); host != "" {
		hosts = appendUnique(hosts, host)
	}
	return hosts
}

// MissingScopes returns the scopes of svc's preset, for its access, that
// granted lacks. A service without a preset misses none.
func (c *Connection) MissingScopes(svc Service, granted []string) []string {
	p, ok := LookupPreset(c.Provider, svc.Name)
	if !ok {
		return nil
	}
	var missing []string
	for _, scope := range p.Scopes(svc.Access) {
		if !slices.Contains(granted, scope) {
			missing = append(missing, scope)
		}
	}
	return missing
}

func googleScopes(names ...string) []string {
	scopes := make([]string, len(names))
	for i, name := range names {
		scopes[i] = googleScopePrefix + name
	}
	return scopes
}

func urlHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Host
}

func appendUnique(list []string, items ...string) []string {
	for _, item := range items {
		if !slices.Contains(list, item) {
			list = append(list, item)
		}
	}
	return list
}

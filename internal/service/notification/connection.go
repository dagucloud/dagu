// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package notification

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/eventstore"
	notificationmodel "github.com/dagucloud/dagu/v2/internal/notification"
	"github.com/dagucloud/dagu/v2/internal/service/chatbridge"
)

// connectionsPagePath is the UI page that lists connections. A
// connection's page is below it, with its workspace as a query parameter.
const connectionsPagePath = "/connections"

// connectionRoute is a route destination of a connection event, its
// channel, and the workspaces whose routes reach that channel.
type connectionRoute struct {
	destination string
	channelID   string
	workspaces  []string
}

// connectionRoutes returns the routes of a connection event: the routes of
// each workspace the event names, with at most one route per channel, so
// that each channel gets one message. Workspaces are visited in name
// order, so a channel keeps the same route from one call to the next.
func (s *Service) connectionRoutes(event chatbridge.NotificationEvent) []connectionRoute {
	if event.Connection == nil || !s.reusableChannelsAllowed() {
		return nil
	}
	ctx := context.Background()
	byChannel := map[string]int{}
	var routes []connectionRoute
	for _, workspaceName := range connectionWorkspaces(event.Connection) {
		routeSet := s.effectiveRouteSet(ctx, workspaceName)
		for _, destination := range s.routeSetDestinationsForEvent(routeSet, event, map[string]struct{}{}) {
			route, _ := findRoute(routeSet, parseRouteDestinationID(destination).RouteID)
			if i, ok := byChannel[route.ChannelID]; ok {
				routes[i].workspaces = append(routes[i].workspaces, workspaceName)
				continue
			}
			byChannel[route.ChannelID] = len(routes)
			routes = append(routes, connectionRoute{destination: destination, channelID: route.ChannelID, workspaces: []string{workspaceName}})
		}
	}
	return routes
}

// connectionCauseChannels returns the channels that send no failure
// message for event's run: for a run whose recorded cause is a broken
// connection, each channel that receives connection.needs_attention
// through the routes of the run's workspace, which the cause's connection
// events also go through (Spec 107 §Events). The decision reads only the
// run's record, so a run read after a reconnect is decided the same way.
func (s *Service) connectionCauseChannels(ctx context.Context, event chatbridge.NotificationEvent) map[string]struct{} {
	channelIDs := map[string]struct{}{}
	if event.Type != eventstore.TypeDAGRunFailed || len(event.Status.ConnectionCause()) == 0 || !s.reusableChannelsAllowed() {
		return channelIDs
	}
	routeSet := s.effectiveRouteSetForEvent(ctx, event)
	if routeSet == nil || !routeSet.Enabled {
		return channelIDs
	}
	for _, route := range routeSet.Routes {
		if notificationmodel.IsRouteEventEnabled(routeSet, route, eventstore.TypeConnectionNeedsAttention) {
			channelIDs[route.ChannelID] = struct{}{}
		}
	}
	return channelIDs
}

// connectionDestinations returns the destinations of connectionRoutes.
func (s *Service) connectionDestinations(event chatbridge.NotificationEvent) []string {
	routes := s.connectionRoutes(event)
	destinations := make([]string, 0, len(routes))
	for _, route := range routes {
		destinations = append(destinations, route.destination)
	}
	return destinations
}

// connectionEventFor returns event as destination receives it on
// channelID, naming only the connections of the workspaces whose routes
// reach that channel, so that no channel learns another workspace's
// connections. A destination whose route now names another channel
// receives nothing.
func (s *Service) connectionEventFor(event chatbridge.NotificationEvent, destination, channelID string) (chatbridge.NotificationEvent, bool) {
	for _, route := range s.connectionRoutes(event) {
		if route.destination != destination || route.channelID != channelID {
			continue
		}
		change := *event.Connection
		change.Connections = make([]eventstore.ConnectionName, 0, len(event.Connection.Connections))
		for _, conn := range event.Connection.Connections {
			if slices.Contains(route.workspaces, conn.Workspace) {
				change.Connections = append(change.Connections, conn)
			}
		}
		event.Connection = &change
		return event, true
	}
	return event, false
}

// sortOldestFirst orders connection events as they happened, so that a
// channel's last message tells a connection's current status.
func sortOldestFirst(events []chatbridge.NotificationEvent) {
	slices.SortStableFunc(events, func(a, b chatbridge.NotificationEvent) int {
		if c := a.ObservedAt.Compare(b.ObservedAt); c != 0 {
			return c
		}
		return strings.Compare(a.Key, b.Key)
	})
}

// connectionWorkspaces returns the workspaces of change's connections,
// sorted and without repeats.
func connectionWorkspaces(change *eventstore.ConnectionChange) []string {
	workspaces := make([]string, 0, len(change.Connections))
	for _, conn := range change.Connections {
		workspaces = append(workspaces, conn.Workspace)
	}
	slices.Sort(workspaces)
	return slices.Compact(workspaces)
}

// plainTextTarget returns target without its message templates, so that a
// connection event is sent as the same plain text on every channel.
func plainTextTarget(target notificationmodel.Target) notificationmodel.Target {
	if target.Email != nil {
		email := *target.Email
		email.SubjectTemplate, email.BodyTemplate, email.AttachLogs = "", "", false
		target.Email = &email
	}
	if target.Webhook != nil {
		webhook := *target.Webhook
		webhook.MessageTemplate, webhook.BodyTemplate = "", ""
		target.Webhook = &webhook
	}
	if target.Slack != nil {
		slack := *target.Slack
		slack.MessageTemplate = ""
		target.Slack = &slack
	}
	if target.Telegram != nil {
		telegram := *target.Telegram
		telegram.MessageTemplate = ""
		target.Telegram = &telegram
	}
	if target.Teams != nil {
		teams := *target.Teams
		teams.MessageTemplate = ""
		target.Teams = &teams
	}
	return target
}

// connectionTitle is the subject of a connection event's message.
func connectionTitle(event chatbridge.NotificationEvent) string {
	change := event.Connection
	subject := "Connection " + connectionNames(change.Connections)
	if change.AppID != "" {
		subject = "OAuth app " + change.AppName
	}
	if event.Type == eventstore.TypeConnectionRestored {
		return subject + " restored"
	}
	return subject + " needs attention"
}

// connectionText is the plain-text message of a connection event.
func connectionText(event chatbridge.NotificationEvent, publicURL string) string {
	change := event.Connection
	restored := event.Type == eventstore.TypeConnectionRestored
	var b strings.Builder
	switch {
	case change.AppID != "" && restored:
		fmt.Fprintf(&b, "OAuth app %q has a new client ID or secret. Its connections try it on their next token request: %s.", change.AppName, connectionNames(change.Connections))
	case change.AppID != "":
		fmt.Fprintf(&b, "OAuth app %q was refused by its provider. Until an admin changes its client ID or secret, its connections cannot get tokens: %s.", change.AppName, connectionNames(change.Connections))
	case restored:
		fmt.Fprintf(&b, "Connection %s is connected again.", connectionNames(change.Connections))
	default:
		fmt.Fprintf(&b, "Connection %s needs to be reconnected.", connectionNames(change.Connections))
	}
	if restored {
		return b.String()
	}
	if change.Reason != "" {
		fmt.Fprintf(&b, "\nReason: %s", change.Reason)
	}
	b.WriteString("\n")
	b.WriteString(connectionAction(change, publicURL))
	return b.String()
}

// connectionAction says where to fix a connection or app that needs
// attention: its page under a usable public URL, or else the Connections
// page and the CLI command.
func connectionAction(change *eventstore.ConnectionChange, publicURL string) string {
	base := normalizeNotificationPublicURL(publicURL)
	if change.AppID != "" || len(change.Connections) != 1 {
		if base == "" {
			return "Fix it on the Connections page."
		}
		return "Fix it: " + base + connectionsPagePath
	}
	conn := change.Connections[0]
	if base == "" {
		command := "dagu connection reconnect " + conn.Name
		if conn.Workspace != "" {
			command += " --workspace " + conn.Workspace
		}
		return "Reconnect it on the Connections page, or run: " + command
	}
	return "Reconnect: " + connectionURL(base, conn)
}

// connectionURL returns the page of conn under base.
func connectionURL(base string, conn eventstore.ConnectionName) string {
	link := base + connectionsPagePath + "/" + url.PathEscape(conn.Name)
	if conn.Workspace != "" {
		link += "?" + url.Values{"workspace": {conn.Workspace}}.Encode()
	}
	return link
}

// connectionNames lists connections as name, or name (workspace).
func connectionNames(conns []eventstore.ConnectionName) string {
	names := make([]string, 0, len(conns))
	for _, conn := range conns {
		if conn.Workspace == "" {
			names = append(names, conn.Name)
			continue
		}
		names = append(names, conn.Name+" ("+conn.Workspace+")")
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

// connectionWebhookItem is a connection event's entry in a webhook payload.
func connectionWebhookItem(event chatbridge.NotificationEvent, publicURL string) map[string]any {
	change := event.Connection
	conns := make([]map[string]string, 0, len(change.Connections))
	for _, conn := range change.Connections {
		conns = append(conns, map[string]string{"workspace": conn.Workspace, "name": conn.Name})
	}
	item := map[string]any{
		"eventType":   string(event.Type),
		"status":      change.Status,
		"connections": conns,
		"observedAt":  event.ObservedAt.Format(time.RFC3339Nano),
	}
	if change.AppID != "" {
		item["app"] = map[string]string{"id": change.AppID, "name": change.AppName}
	}
	if change.Reason != "" {
		item["reason"] = change.Reason
	}
	if base := normalizeNotificationPublicURL(publicURL); base != "" && change.AppID == "" && len(change.Connections) == 1 {
		item["connectionUrl"] = connectionURL(base, change.Connections[0])
	}
	return item
}

// connectionTemplateValues are the values a command channel receives for
// a connection event. STATUS and ERROR carry the new status and the
// provider's reason.
func connectionTemplateValues(event chatbridge.NotificationEvent, values map[string]string) {
	change := event.Connection
	values["run.status"] = change.Status
	values["status"] = change.Status
	values["run.error"] = change.Reason
	values["error"] = change.Reason
	if workspaces := connectionWorkspaces(change); len(workspaces) == 1 {
		values["workspace"] = workspaces[0]
	}
}

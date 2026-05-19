// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"fmt"
	"log/slog"
	"net"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/dagucloud/dagu/internal/agent"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/service/discord"
	"github.com/dagucloud/dagu/internal/service/frontend"
	"github.com/dagucloud/dagu/internal/service/line"
	"github.com/dagucloud/dagu/internal/service/resource"
	daguslack "github.com/dagucloud/dagu/internal/service/slack"
	"github.com/dagucloud/dagu/internal/service/telegram"
	"github.com/dagucloud/dagu/internal/tunnel"
	"github.com/spf13/cobra"
)

func Server() *cobra.Command {
	return NewCommand(
		&cobra.Command{
			Use:   "server [flags]",
			Short: "Start the web UI server for DAG management",
			Long: `Launch the Dagu web server that provides a graphical interface for monitoring and managing DAGs.

The web UI allows you to:
- View and manage DAG definitions
- Monitor active and historical DAG-runs
- Inspect DAG-run details including step status and logs
- Start, stop, and retry DAG-runs 
- View execution history and statistics

Flags:
  --host string    Host address to bind the server to (default: 127.0.0.1)
  --port int       Port number to listen on (default: 8080)
  --dags string    Path to the directory containing DAG definition files

Example:
  dagu server --host=0.0.0.0 --port=8080 --dags=/path/to/dags
`,
		}, serverFlags, runServer,
	)
}

var serverFlags = []commandLineFlag{dagsFlag, hostFlag, portFlag, tunnelFlag, tunnelTokenFlag, tunnelFunnelFlag, tunnelHTTPSFlag}

// runServer initializes and runs the web UI server and its resource monitoring service.
// It logs startup info, starts the resource service (deferring its shutdown and logging any stop errors),
// constructs the server with that resource service, and then begins serving.
// It returns an error if the resource service fails to start, the server fails to initialize, or serving fails.
func runServer(ctx *Context, _ []string) error {
	// Create a context that will be cancelled on interrupt signal.
	// This must be created BEFORE server initialization so auth provider init can be cancelled.
	signalCtx, stop := signal.NotifyContext(ctx.Context, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Create a signal-aware context for services
	serviceCtx := ctx.WithContext(signalCtx)

	// Stop license manager on shutdown
	if ctx.LicenseManager != nil {
		defer ctx.LicenseManager.Stop()
	}

	logger.Info(serviceCtx, "Server initialization",
		tag.Host(serviceCtx.Config.Server.Host),
		tag.Port(serviceCtx.Config.Server.Port),
	)

	// Initialize tunnel service if enabled
	tunnelService, err := initTunnelService(ctx.Config)
	if err != nil {
		return fmt.Errorf("failed to initialize tunnel: %w", err)
	}
	if tunnelService != nil {
		defer func() {
			if err := tunnelService.Stop(ctx); err != nil {
				logger.Error(ctx, "Failed to stop tunnel service", tag.Error(err))
			}
		}()
	}

	// Initialize resource monitoring service (defer cleanup, but don't start yet).
	// Resource monitoring must start AFTER server init to avoid race condition
	// with auth provider initialization (gopsutil conflicts with net/http dial).
	resourceService := resource.NewService(ctx.Config)
	defer func() {
		if err := resourceService.Stop(ctx); err != nil {
			logger.Error(ctx, "Failed to stop resource service", tag.Error(err))
		}
	}()

	// Build server options
	var serverOpts []frontend.ServerOption
	if tunnelService != nil {
		serverOpts = append(serverOpts, frontend.WithTunnelService(tunnelService))
	}

	// If a bot provider is configured, capture the agent API via callback to start the bot.
	var agentAPI *agent.API
	if ctx.Config.Bots.Provider != config.BotProviderNone {
		serverOpts = append(serverOpts, frontend.WithAgentAPICallback(func(api *agent.API) {
			agentAPI = api
		}))
	}

	// Initialize server (includes auth setup). Use serviceCtx so auth providers can
	// respond to termination signals during potentially slow network operations.
	server, err := serviceCtx.NewServer(resourceService, serverOpts...)
	if err != nil {
		return fmt.Errorf("failed to initialize server: %w", err)
	}

	// Start bot if a provider is configured and agent API is available.
	if agentAPI != nil {
		switch ctx.Config.Bots.Provider {
		case config.BotProviderTelegram:
			tgBot, tgErr := telegram.New(
				telegram.Config{
					Token:                 ctx.Config.Bots.Telegram.Token,
					AllowedChatIDs:        ctx.Config.Bots.Telegram.AllowedChatIDs,
					InterestedEventTypes:  ctx.Config.Bots.Telegram.InterestedEventTypes,
					SafeMode:              ctx.Config.Bots.SafeMode,
					EventService:          ctx.EventService,
					NotificationStateFile: filepath.Join(ctx.Config.Paths.DataDir, "bots", "telegram", "notifications.json"),
				},
				agentAPI,
				slog.Default(),
			)
			if tgErr != nil {
				logger.Warn(serviceCtx, "Failed to initialize Telegram bot", tag.Error(tgErr))
			} else {
				go func() {
					if runErr := tgBot.Run(signalCtx); runErr != nil {
						logger.Error(serviceCtx, "Telegram bot failed", tag.Error(runErr))
					}
				}()
				logger.Info(serviceCtx, "Telegram bot started")
			}

		case config.BotProviderSlack:
			slackBot, slackErr := daguslack.New(
				daguslack.Config{
					BotToken:              ctx.Config.Bots.Slack.BotToken,
					AppToken:              ctx.Config.Bots.Slack.AppToken,
					AllowedChannelIDs:     ctx.Config.Bots.Slack.AllowedChannelIDs,
					InterestedEventTypes:  ctx.Config.Bots.Slack.InterestedEventTypes,
					RespondToAll:          ctx.Config.Bots.Slack.RespondToAll,
					SafeMode:              ctx.Config.Bots.SafeMode,
					EventService:          ctx.EventService,
					NotificationStateFile: filepath.Join(ctx.Config.Paths.DataDir, "bots", "slack", "notifications.json"),
				},
				agentAPI,
				slog.Default(),
			)
			if slackErr != nil {
				logger.Warn(serviceCtx, "Failed to initialize Slack bot", tag.Error(slackErr))
			} else {
				go func() {
					if runErr := slackBot.Run(signalCtx); runErr != nil {
						logger.Error(serviceCtx, "Slack bot failed", tag.Error(runErr))
					}
				}()
				logger.Info(serviceCtx, "Slack bot started")
			}

		case config.BotProviderDiscord:
			discordBot, discordErr := discord.New(
				discord.Config{
					Token:                 ctx.Config.Bots.Discord.Token,
					AllowedChannelIDs:     ctx.Config.Bots.Discord.AllowedChannelIDs,
					InterestedEventTypes:  ctx.Config.Bots.Discord.InterestedEventTypes,
					RespondToAll:          ctx.Config.Bots.Discord.RespondToAll,
					SafeMode:              ctx.Config.Bots.SafeMode,
					EventService:          ctx.EventService,
					NotificationStateFile: filepath.Join(ctx.Config.Paths.DataDir, "bots", "discord", "notifications.json"),
				},
				agentAPI,
				slog.Default(),
			)
			if discordErr != nil {
				logger.Warn(serviceCtx, "Failed to initialize Discord bot", tag.Error(discordErr))
			} else {
				go func() {
					if runErr := discordBot.Run(signalCtx); runErr != nil {
						logger.Error(serviceCtx, "Discord bot failed", tag.Error(runErr))
					}
				}()
				logger.Info(serviceCtx, "Discord bot started")
			}

		case config.BotProviderLine:
			lineBot, lineErr := line.New(
				line.Config{
					ChannelAccessToken:    ctx.Config.Bots.Line.ChannelAccessToken,
					ChannelSecret:         ctx.Config.Bots.Line.ChannelSecret,
					AllowedSourceIDs:      ctx.Config.Bots.Line.AllowedSourceIDs,
					InterestedEventTypes:  ctx.Config.Bots.Line.InterestedEventTypes,
					RespondToAll:          ctx.Config.Bots.Line.RespondToAll,
					SafeMode:              ctx.Config.Bots.SafeMode,
					EventService:          ctx.EventService,
					NotificationStateFile: filepath.Join(ctx.Config.Paths.DataDir, "bots", "line", "notifications.json"),
				},
				agentAPI,
				slog.Default(),
			)
			if lineErr != nil {
				logger.Warn(serviceCtx, "Failed to initialize LINE bot", tag.Error(lineErr))
			} else {
				server.RegisterRoutes(lineBot.ConfigureRoutes)
				go func() {
					if runErr := lineBot.Run(signalCtx); runErr != nil {
						logger.Error(serviceCtx, "LINE bot failed", tag.Error(runErr))
					}
				}()
				logger.Info(serviceCtx, "LINE bot started")
			}

		case config.BotProviderNone:
			// No bot configured
		}
	}

	// Start resource monitoring now that server initialization is complete.
	if err := resourceService.Start(serviceCtx); err != nil {
		return fmt.Errorf("failed to start resource service: %w", err)
	}

	// Start tunnel service after server is initialized
	if tunnelService != nil {
		localAddr := net.JoinHostPort(ctx.Config.Server.Host, strconv.Itoa(ctx.Config.Server.Port))
		if err := tunnelService.Start(serviceCtx, localAddr); err != nil {
			// Log warning but continue without tunnel - graceful degradation
			logger.Warn(serviceCtx, "Tunnel failed to start (server will continue without tunnel)",
				tag.Error(err),
			)
		} else {
			// Log tunnel URL prominently on success
			logTunnelStatus(serviceCtx, tunnelService)
		}
	}

	if err := server.Serve(serviceCtx); err != nil {
		return fmt.Errorf("failed to start server: %w", err)
	}

	return nil
}

// initTunnelService creates and returns a tunnel service based on configuration.
// Returns nil if tunneling is not enabled.
func initTunnelService(cfg *config.Config) (*tunnel.Service, error) {
	if !cfg.Tunnel.Enabled {
		return nil, nil
	}

	tc := cfg.Tunnel
	tunnelCfg := &tunnel.Config{
		Enabled:       tc.Enabled,
		AllowTerminal: tc.AllowTerminal,
		AllowedIPs:    tc.AllowedIPs,
		Tailscale: tunnel.TailscaleConfig{
			AuthKey:  tc.Tailscale.AuthKey,
			Hostname: tc.Tailscale.Hostname,
			Funnel:   tc.Tailscale.Funnel,
			HTTPS:    tc.Tailscale.HTTPS,
			StateDir: tc.Tailscale.StateDir,
		},
		RateLimiting: tunnel.RateLimitConfig{
			Enabled:              tc.RateLimiting.Enabled,
			LoginAttempts:        tc.RateLimiting.LoginAttempts,
			WindowSeconds:        tc.RateLimiting.WindowSeconds,
			BlockDurationSeconds: tc.RateLimiting.BlockDurationSeconds,
		},
	}

	return tunnel.NewService(tunnelCfg, cfg.Paths.DataDir)
}

// logTunnelStatus logs the tunnel status prominently to the console.
func logTunnelStatus(ctx *Context, svc *tunnel.Service) {
	info := svc.Info()

	accessType := "Private (tailnet only)"
	if info.IsPublic {
		accessType = "Public"
	}

	var authStatus string
	switch ctx.Config.Server.Auth.Mode {
	case config.AuthModeBuiltin:
		authStatus = "Builtin (enabled)"
	case config.AuthModeBasic:
		authStatus = "Basic (enabled)"
	case config.AuthModeNone:
		authStatus = "Disabled"
	default:
		authStatus = fmt.Sprintf("Unknown (%s)", ctx.Config.Server.Auth.Mode)
	}

	terminalStatus := "Disabled"
	if ctx.Config.Tunnel.AllowTerminal && ctx.Config.Server.Terminal.Enabled {
		terminalStatus = "Enabled"
	}

	// Print a prominent banner for tunnel status
	fmt.Printf("\n"+
		"━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n"+
		"  TUNNEL ACTIVE - Server is %s accessible\n"+
		"━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n"+
		"  Provider: %s (%s)\n"+
		"  URL:      %s\n"+
		"  Auth:     %s\n"+
		"  Terminal: %s\n"+
		"━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n",
		accessType,
		info.Provider, info.Mode,
		info.PublicURL,
		authStatus,
		terminalStatus,
	)
}

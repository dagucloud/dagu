// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package frontend

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"

	apiv1 "github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/license"
	"github.com/dagucloud/dagu/internal/service/frontend/api/pathutil"
	workspacepkg "github.com/dagucloud/dagu/internal/workspace"
)

//go:embed templates/* assets/*
var assetsFS embed.FS

const (
	templatePath     = "templates/"
	baseTemplateName = "base"
	baseTemplateFile = "base.gohtml"
)

var (
	// TODO: Cache only the bundle hash here once when we can revisit this
	// package-global state without widening this low-risk regression fix.
	assetVersionOnce sync.Once
	assetVersion     string
)

func formatAssetVersion(version string, bundle []byte) string {
	sum := sha256.Sum256(bundle)
	suffix := hex.EncodeToString(sum[:8])
	if version == "" {
		return suffix
	}
	return version + "-" + suffix
}

func currentAssetVersion() string {
	assetVersionOnce.Do(func() {
		data, err := assetsFS.ReadFile("assets/bundle.js")
		if err != nil {
			assetVersion = config.Version
			return
		}
		assetVersion = formatAssetVersion(config.Version, data)
	})
	return assetVersion
}

func (srv *Server) useTemplate(ctx context.Context, layout, name string) func(http.ResponseWriter, any) {
	if srv.config.Server.Headless {
		return func(w http.ResponseWriter, _ any) {
			http.Error(w, "Web UI is disabled in headless mode", http.StatusForbidden)
		}
	}

	files := []string{
		path.Join(templatePath, baseTemplateFile),
		path.Join(templatePath, layout),
	}
	tmpl, err := template.New(name).Funcs(defaultFunctions(&srv.funcsConfig)).ParseFS(assetsFS, files...)
	if err != nil {
		panic(err)
	}

	return func(w http.ResponseWriter, data any) {
		rendered, err := executeTemplate(tmpl, baseTemplateName, data)
		if err != nil {
			logger.Error(ctx, "Template execution failed", tag.Error(err))
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, rendered)
	}
}

func executeTemplate(tmpl *template.Template, name string, data any) (*bytes.Buffer, error) {
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		return nil, err
	}
	return &buf, nil
}

// SetupRequiredChecker determines whether initial admin setup is still needed.
// Called on every HTML page render so the value is always up-to-date.
type SetupRequiredChecker interface {
	IsSetupRequired(ctx context.Context) bool
}

// UpdateChecker provides dynamic update availability for template rendering.
// Called on every HTML page render so the banner reflects the latest cached check.
type UpdateChecker interface {
	GetUpdateInfo() (updateAvailable bool, latestVersion string)
}

// AgentEnabledChecker provides the agent enabled status for template rendering.
type AgentEnabledChecker interface {
	IsEnabled(ctx context.Context) bool
}

type funcsConfig struct {
	NavbarColor           string
	NavbarTitle           string
	BasePath              string
	APIBasePath           string
	TZ                    string
	TzOffsetInSec         int
	MaxDashboardPageLimit int
	RemoteNodes           []string
	Permissions           map[config.Permission]bool
	Paths                 config.PathsConfig
	AuthMode              config.AuthMode
	OIDCEnabled           bool
	OIDCButtonLabel       string
	TerminalEnabled       bool
	GitSyncEnabled        bool
	WorkspaceStore        workspacepkg.Store

	SetupRequiredChecker SetupRequiredChecker
	UpdateChecker        UpdateChecker
	AgentEnabledChecker  AgentEnabledChecker
	LicenseChecker       license.Checker
	LicenseManager       *license.Manager
}

func defaultFunctions(cfg *funcsConfig) template.FuncMap {
	boolStr := func(b bool) string { return strconv.FormatBool(b) }

	return template.FuncMap{
		"defTitle":              func(v any) string { s, _ := v.(string); return s },
		"version":               func() string { return config.Version },
		"assetVersion":          currentAssetVersion,
		"navbarColor":           func() string { return cfg.NavbarColor },
		"navbarTitle":           func() string { return cfg.NavbarTitle },
		"basePath":              func() string { return cfg.BasePath },
		"apiURL":                func() string { return pathutil.BuildMountedAPIPath(cfg.BasePath, cfg.APIBasePath) },
		"tz":                    func() string { return cfg.TZ },
		"tzOffsetInSec":         func() int { return cfg.TzOffsetInSec },
		"maxDashboardPageLimit": func() int { return cfg.MaxDashboardPageLimit },
		"remoteNodes":           func() string { return strings.Join(cfg.RemoteNodes, ",") },
		"authMode":              func() string { return string(cfg.AuthMode) },
		"oidcButtonLabel":       func() string { return cfg.OIDCButtonLabel },
		"initialWorkspacesJSON": func() string {
			if cfg.WorkspaceStore == nil {
				return "[]"
			}
			workspaces, err := cfg.WorkspaceStore.List(context.Background())
			if err != nil {
				return "[]"
			}
			response := make([]apiv1.WorkspaceResponse, 0, len(workspaces))
			for _, ws := range workspaces {
				if ws == nil {
					continue
				}
				item := apiv1.WorkspaceResponse{
					Id:   ws.ID,
					Name: ws.Name,
				}
				if ws.Description != "" {
					item.Description = &ws.Description
				}
				if !ws.CreatedAt.IsZero() {
					item.CreatedAt = &ws.CreatedAt
				}
				if !ws.UpdatedAt.IsZero() {
					item.UpdatedAt = &ws.UpdatedAt
				}
				response = append(response, item)
			}
			data, err := json.Marshal(response)
			if err != nil {
				return "[]"
			}
			return string(data)
		},

		// Permission functions
		"permissionsWriteDags": func() string { return boolStr(cfg.Permissions[config.PermissionWriteDAGs]) },
		"permissionsRunDags":   func() string { return boolStr(cfg.Permissions[config.PermissionRunDAGs]) },

		// Feature toggle functions
		"oidcEnabled": func() string {
			if !cfg.OIDCEnabled {
				return "false"
			}
			if cfg.LicenseChecker != nil && !cfg.LicenseChecker.IsFeatureEnabled(license.FeatureSSO) {
				return "false"
			}
			return "true"
		},
		"terminalEnabled": func() string { return boolStr(cfg.TerminalEnabled) },
		"gitSyncEnabled":  func() string { return boolStr(cfg.GitSyncEnabled) },
		"agentEnabled": func() string {
			if cfg.AgentEnabledChecker == nil {
				return "false"
			}
			return boolStr(cfg.AgentEnabledChecker.IsEnabled(context.Background()))
		},

		"setupRequired": func() string {
			if cfg.SetupRequiredChecker == nil {
				return "false"
			}
			return boolStr(cfg.SetupRequiredChecker.IsSetupRequired(context.Background()))
		},
		"updateAvailable": func() string {
			if cfg.UpdateChecker == nil {
				return "false"
			}
			available, _ := cfg.UpdateChecker.GetUpdateInfo()
			return boolStr(available)
		},
		"latestVersion": func() string {
			if cfg.UpdateChecker == nil {
				return ""
			}
			_, version := cfg.UpdateChecker.GetUpdateInfo()
			return version
		},

		// License functions
		"licenseValid": func() string {
			if cfg.LicenseChecker == nil || cfg.LicenseChecker.IsCommunity() {
				return "false"
			}
			claims := cfg.LicenseChecker.Claims()
			if claims.ExpiresAt == nil {
				return "true" // perpetual
			}
			if claims.ExpiresAt.After(time.Now()) {
				return "true"
			}
			return "false"
		},
		"licensePlan": func() string {
			if cfg.LicenseChecker == nil {
				return ""
			}
			return cfg.LicenseChecker.Plan()
		},
		"licenseExpiry": func() string {
			if cfg.LicenseChecker == nil {
				return ""
			}
			claims := cfg.LicenseChecker.Claims()
			if claims == nil || claims.ExpiresAt == nil {
				return ""
			}
			return claims.ExpiresAt.Format("2006-01-02T15:04:05Z")
		},
		"licenseFeatures": func() string {
			if cfg.LicenseChecker == nil {
				return "[]"
			}
			claims := cfg.LicenseChecker.Claims()
			if claims == nil || len(claims.Features) == 0 {
				return "[]"
			}
			b, err := json.Marshal(claims.Features)
			if err != nil {
				return "[]"
			}
			return string(b)
		},
		"licenseGracePeriod": func() string {
			if cfg.LicenseChecker == nil {
				return "false"
			}
			return boolStr(cfg.LicenseChecker.IsGracePeriod())
		},
		"licenseGraceEndsAt": func() string {
			if cfg.LicenseChecker == nil {
				return ""
			}
			return graceEndsAt(cfg.LicenseChecker.Claims())
		},
		"licenseCommunity": func() string {
			if cfg.LicenseChecker == nil {
				return "true"
			}
			return boolStr(cfg.LicenseChecker.IsCommunity())
		},
		"licenseWarningCode": func() string {
			if cfg.LicenseChecker == nil {
				return ""
			}
			return cfg.LicenseChecker.WarningCode()
		},
		"licenseSource": func() string {
			if cfg.LicenseManager == nil {
				return ""
			}
			if cfg.LicenseManager.Source().IsEnv() {
				return "env"
			}
			if cfg.LicenseManager.Source() == license.SourceNone {
				return ""
			}
			return "file"
		},

		// Path configuration functions
		"pathDAGsDir":            func() string { return cfg.Paths.DAGsDir },
		"pathLogDir":             func() string { return cfg.Paths.LogDir },
		"pathSuspendFlagsDir":    func() string { return cfg.Paths.SuspendFlagsDir },
		"pathAdminLogsDir":       func() string { return cfg.Paths.AdminLogsDir },
		"pathBaseConfig":         func() string { return cfg.Paths.BaseConfig },
		"pathDAGRunsDir":         func() string { return cfg.Paths.DAGRunsDir },
		"pathQueueDir":           func() string { return cfg.Paths.QueueDir },
		"pathProcDir":            func() string { return cfg.Paths.ProcDir },
		"pathServiceRegistryDir": func() string { return cfg.Paths.ServiceRegistryDir },
		"pathConfigFileUsed":     func() string { return cfg.Paths.ConfigFileUsed },
		"pathUsersDir":           func() string { return cfg.Paths.UsersDir },
		"pathGitSyncDir":         func() string { return path.Join(cfg.Paths.DataDir, "gitsync") },
		"pathAuditLogsDir":       func() string { return path.Join(cfg.Paths.AdminLogsDir, "audit") },
	}
}

func graceEndsAt(claims *license.LicenseClaims) string {
	if claims == nil || claims.ExpiresAt == nil {
		return ""
	}

	graceDays := 14
	if claims.GraceDays != nil {
		graceDays = max(*claims.GraceDays, 0)
	}

	return claims.ExpiresAt.Time.Add(time.Duration(graceDays) * 24 * time.Hour).Format("2006-01-02T15:04:05Z")
}

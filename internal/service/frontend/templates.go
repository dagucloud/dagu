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

	apiv1 "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/license"
	"github.com/dagucloud/dagu/v2/internal/service/frontend/api/pathutil"
	workspacepkg "github.com/dagucloud/dagu/v2/internal/workspace"
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
	ProxyEnabled          bool
	ProxyButtonLabel      string
	TerminalEnabled       bool
	GitSyncEnabled        bool
	WorkspaceStore        workspacepkg.Store

	SetupRequiredChecker SetupRequiredChecker
	UpdateChecker        UpdateChecker
	LicenseChecker       license.Checker
	LicenseManager       *license.Manager
}

func defaultFunctions(cfg *funcsConfig) template.FuncMap {
	boolStr := func(b bool) string { return strconv.FormatBool(b) }
	licenseStatus := func() license.Status {
		if cfg.LicenseManager != nil {
			return cfg.LicenseManager.Status()
		}
		return license.StatusFor(cfg.LicenseChecker)
	}

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
		"proxyButtonLabel":      func() string { return cfg.ProxyButtonLabel },
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
		"proxyEnabled": func() string {
			if !cfg.ProxyEnabled {
				return "false"
			}
			if cfg.LicenseChecker != nil && !cfg.LicenseChecker.IsFeatureEnabled(license.FeatureSSO) {
				return "false"
			}
			return "true"
		},
		"terminalEnabled": func() string { return boolStr(cfg.TerminalEnabled) },
		"gitSyncEnabled":  func() string { return boolStr(cfg.GitSyncEnabled) },

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
			return boolStr(licenseStatus().Valid)
		},
		"licensePlan": func() string {
			return licenseStatus().Plan
		},
		"licenseExpiry": func() string {
			status := licenseStatus()
			if status.Expiry.IsZero() {
				return ""
			}
			return status.Expiry.Format("2006-01-02T15:04:05Z")
		},
		"licenseFeatures": func() string {
			features := licenseStatus().Features
			if len(features) == 0 {
				return "[]"
			}
			b, err := json.Marshal(features)
			if err != nil {
				return "[]"
			}
			return string(b)
		},
		"licenseGracePeriod": func() string {
			return boolStr(licenseStatus().GracePeriod)
		},
		"licenseGraceEndsAt": func() string {
			status := licenseStatus()
			if status.GraceEndsAt.IsZero() {
				return ""
			}
			return status.GraceEndsAt.Format("2006-01-02T15:04:05Z")
		},
		"licenseCommunity": func() string {
			return boolStr(licenseStatus().Community)
		},
		"licenseWarningCode": func() string {
			return licenseStatus().WarningCode
		},
		"licenseError": func() string {
			return licenseStatus().Failure
		},
		"licenseSource": func() string {
			source := licenseStatus().Source
			if source.IsEnv() {
				return "env"
			}
			if source == license.SourceNone {
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
		"pathWikiDir":            func() string { return cfg.Paths.WikiDir },
		"pathDocsDir":            func() string { return cfg.Paths.WikiDir },
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

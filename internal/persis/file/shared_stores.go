// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dagucloud/dagu/v2/internal/aiendpoint"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/crypto"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/dagsettings"
	"github.com/dagucloud/dagu/v2/internal/incident"
	"github.com/dagucloud/dagu/v2/internal/license"
	"github.com/dagucloud/dagu/v2/internal/notification"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/profile"
	"github.com/dagucloud/dagu/v2/internal/secret"
	"github.com/dagucloud/dagu/v2/internal/upgrade"
)

// NewSecretStore wires the encrypted file-backed secret store from config paths.
func NewSecretStore(ctx context.Context, cfg *config.Config, col persis.Collection) secret.Store {
	if cfg == nil || cfg.Paths.DataDir == "" {
		return nil
	}
	if encKey, encErr := crypto.ResolveKey(cfg.Paths.DataDir); encErr != nil {
		logger.Warn(ctx, "Failed to resolve encryption key for secret store", tag.Error(encErr))
	} else if enc, encErr := crypto.NewEncryptor(encKey); encErr != nil {
		logger.Warn(ctx, "Failed to create encryptor for secret store", tag.Error(encErr))
	} else if secretStore, storeErr := store.NewSecretStore(col, enc); storeErr != nil {
		logger.Warn(ctx, "Failed to create secret store", tag.Error(storeErr))
	} else {
		return secretStore
	}
	return nil
}

// NewProfileStore wires the file-backed runtime profile store from config paths.
func NewProfileStore(ctx context.Context, cfg *config.Config, col persis.Collection) profile.Store {
	if cfg == nil || cfg.Paths.DataDir == "" {
		return nil
	}
	profileStore, err := store.NewProfileStore(col)
	if err != nil {
		logger.Warn(ctx, "Failed to create profile store", tag.Error(err))
		return nil
	}
	return profileStore
}

// NewAIEndpointService wires the AI endpoint service (Spec 093). The metadata of
// the endpoints is in col. The keys are managed secrets of secrets. It returns
// nil when secrets is nil, because a key needs the secret store.
func NewAIEndpointService(ctx context.Context, col persis.Collection, secrets secret.Store) *aiendpoint.Service {
	if secrets == nil {
		return nil
	}
	endpointStore, err := store.NewAIEndpointStore(col)
	if err != nil {
		logger.Warn(ctx, "Failed to create AI endpoint store", tag.Error(err))
		return nil
	}
	service, err := aiendpoint.NewService(endpointStore, secrets)
	if err != nil {
		logger.Warn(ctx, "Failed to create AI endpoint service", tag.Error(err))
		return nil
	}
	return service
}

// NewAIMCPServerService wires the MCP server entry service (Spec 095). The metadata
// of the entries is in col. The credentials are managed secrets of secrets. It
// returns nil when secrets is nil, because a credential needs the secret store.
func NewAIMCPServerService(ctx context.Context, col persis.Collection, secrets secret.Store) *aiendpoint.MCPService {
	if secrets == nil {
		return nil
	}
	serverStore, err := store.NewAIMCPServerStore(col)
	if err != nil {
		logger.Warn(ctx, "Failed to create MCP server entry store", tag.Error(err))
		return nil
	}
	service, err := aiendpoint.NewMCPService(serverStore, secrets)
	if err != nil {
		logger.Warn(ctx, "Failed to create MCP server entry service", tag.Error(err))
		return nil
	}
	return service
}

// EnableAIEndpointModels gives the service its models store (Spec 093, "The
// models list"). The lists are in col. The reader calls the providers. A failure
// is logged, and the service then has no lists.
func EnableAIEndpointModels(ctx context.Context, service *aiendpoint.Service, col persis.Collection, reader *aiendpoint.ModelsReader) {
	if service == nil {
		return
	}
	modelsStore, err := store.NewAIEndpointModelsStore(col)
	if err == nil {
		err = service.EnableModels(modelsStore, reader)
	}
	if err != nil {
		logger.Warn(ctx, "Failed to enable the AI models lists", tag.Error(err))
	}
}

// AIEndpointService opens the AI endpoint service (Spec 093), which every
// project shares. It returns nil when secrets is nil.
func (s *ServerStores) AIEndpointService(ctx context.Context, secrets secret.Store) *aiendpoint.Service {
	return NewAIEndpointService(ctx, s.backend.Collection(persis.CollectionAIEndpoints), secrets)
}

// AIMCPServerService opens the MCP server entry service (Spec 095), which
// every project shares. It returns nil when secrets is nil.
func (s *ServerStores) AIMCPServerService(ctx context.Context, secrets secret.Store) *aiendpoint.MCPService {
	return NewAIMCPServerService(ctx, s.backend.Collection(persis.CollectionAIMCPServers), secrets)
}

// EnableAIEndpointModels gives service its models lists (Spec 093, "The
// models list"), which reader fetches from the providers.
func (s *ServerStores) EnableAIEndpointModels(ctx context.Context, service *aiendpoint.Service, reader *aiendpoint.ModelsReader) {
	EnableAIEndpointModels(ctx, service, s.backend.Collection(persis.CollectionAIEndpointModels), reader)
}

func NewDAGSettingsStore(cfg *config.Config, col persis.Collection) (dagsettings.Store, error) {
	if cfg == nil || cfg.Paths.DataDir == "" {
		return nil, fmt.Errorf("DAG settings store: DataDir cannot be empty")
	}
	if err := createCollectionDirs(col, "DAG settings store", 0o750, ""); err != nil {
		return nil, err
	}
	return store.NewDAGSettingsStore(col)
}

// NewIncidentStore creates an incident store that keeps a project's policies
// and states in col and the server-wide providers in providers.
func NewIncidentStore(col, providers persis.Collection, enc *crypto.Encryptor) (incident.Store, error) {
	if err := createCollectionDirs(providers, "incident store", 0o750, "", "providers"); err != nil {
		return nil, err
	}
	if err := createCollectionDirs(
		col,
		"incident store",
		0o750,
		"",
		"policies/workspaces",
		"policies/dags",
		"states",
	); err != nil {
		return nil, err
	}
	providerStore, err := store.NewIncidentProviderStore(providers, enc)
	if err != nil {
		return nil, err
	}
	return store.NewIncidentStore(col, providerStore)
}

// NewNotificationStore creates a notification store that keeps a project's
// routes in col and the server-wide channels and notification settings in
// channels.
func NewNotificationStore(col, channels persis.Collection, enc *crypto.Encryptor) (notification.Store, error) {
	if err := createCollectionDirs(channels, "notification store", 0o750, "", "channels"); err != nil {
		return nil, err
	}
	if err := createCollectionDirs(col, "notification store", 0o750, "", "dags", "routes/workspaces"); err != nil {
		return nil, err
	}
	channelStore, err := store.NewNotificationChannelStore(channels, enc)
	if err != nil {
		return nil, err
	}
	return store.NewNotificationStore(col, channelStore, enc)
}

// connectionDirPerm keeps the connection collections, which hold secrets,
// owner-only.
const connectionDirPerm os.FileMode = 0o700

// NewConnectionStore creates the store of OAuth apps, connections, their
// token state, and their tool catalogs. Each collection's directory is
// owner-only, including one that already exists.
func NewConnectionStore(
	apps, conns, tokens, catalogs persis.Collection,
	lifecycle persis.Lock,
	enc *crypto.Encryptor,
) (connection.Store, error) {
	for _, col := range []persis.Collection{apps, conns, tokens, catalogs} {
		if err := createCollectionDirs(col, "connection store", connectionDirPerm, ""); err != nil {
			return nil, err
		}
		if fileCol, ok := col.(*Collection); ok {
			if err := os.Chmod(fileCol.dir, connectionDirPerm); err != nil {
				return nil, fmt.Errorf("connection store: restrict directory %s: %w", fileCol.dir, err)
			}
		}
	}
	return store.NewConnectionStore(apps, conns, tokens, catalogs, lifecycle, enc)
}

func NewLicenseStore(ctx context.Context, col persis.Collection) license.ActivationStore {
	// License data requires an owner-only collection directory.
	if err := createCollectionDirs(col, "license store", 0o700, ""); err != nil {
		logger.Warn(ctx, "Failed to create license store directory", tag.Error(err))
	}
	return store.NewLicenseStore(col)
}

func LicenseDir(cfg *config.Config) string {
	return filepath.Join(cfg.Paths.DataDir, "license")
}

func NewUpgradeCheckStore(cfg *config.Config, col persis.Collection) (upgrade.CacheStore, error) {
	if cfg.Paths.DataDir == "" {
		return nil, fmt.Errorf("upgrade check store: data directory cannot be empty")
	}
	if err := createCollectionDirs(col, "upgrade check store", 0o750, ""); err != nil {
		return nil, err
	}
	return store.NewUpgradeCheckStore(col), nil
}

func createCollectionDirs(col persis.Collection, storeName string, perm os.FileMode, relativePaths ...string) error {
	fileCol, ok := col.(*Collection)
	if !ok {
		return nil
	}
	if fileCol.dir == "" {
		return fmt.Errorf("%s: directory cannot be empty", storeName)
	}
	for _, relativePath := range relativePaths {
		path := filepath.Join(fileCol.dir, filepath.FromSlash(relativePath))
		if err := os.MkdirAll(path, perm); err != nil {
			return fmt.Errorf("%s: create directory %s: %w", storeName, path, err)
		}
	}
	return nil
}

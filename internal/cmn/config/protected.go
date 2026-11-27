// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/project"
)

// CheckProjectDAGsDir returns an error wrapping project.ErrProtectedPath
// that names each protected path that dir, the DAG directory of a project
// other than the default one, would be or contain. The protected paths are the paths that a paths setting other
// than paths.projects_dir names, whether set or defaulted, the default
// project's wiki directory, paths.base_config, and config.yaml, so removing
// the directory never removes the files of the server, the library, or
// another project.
func (c *Config) CheckProjectDAGsDir(dir string) error {
	fs := newLookups()
	located := locateDir(dir, fs)
	var hits []string
	for _, protected := range locatePaths(c.protectedPaths(), fs) {
		if rel := located.relation(protected.dir); rel == relationIs || rel == relationContains {
			hits = append(hits, fmt.Sprintf("%s %q", protected.name, protected.path))
		}
	}
	if len(hits) == 0 {
		return nil
	}
	return fmt.Errorf("%w %q: %s", project.ErrProtectedPath, dir, strings.Join(hits, ", "))
}

func (c *Config) protectedPaths() []namedPath {
	p := c.Paths
	paths := []namedPath{
		{"paths.dags_dir", p.DAGsDir},
		{"paths.alt_dags_dir", p.AltDAGsDir},
		{"paths.data_dir", p.DataDir},
		{"paths.log_dir", p.LogDir},
		{"paths.artifact_dir", p.ArtifactDir},
		{"paths.dag_runs_dir", p.DAGRunsDir},
		{"paths.dag_run_work_dir", p.DAGRunWorkDir},
		{"paths.dag_state_dir", p.DAGStateDir},
		{"paths.event_store_dir", p.EventStoreDir},
		{"paths.suspend_flags_dir", p.SuspendFlagsDir},
		{"paths.queue_dir", p.QueueDir},
		{"paths.proc_dir", p.ProcDir},
		{"paths.service_registry_dir", p.ServiceRegistryDir},
		{"paths.users_dir", p.UsersDir},
		{"paths.api_keys_dir", p.APIKeysDir},
		{"paths.webhooks_dir", p.WebhooksDir},
		{"paths.contexts_dir", p.ContextsDir},
		{"paths.remote_nodes_dir", p.RemoteNodesDir},
		{"paths.workspaces_dir", p.WorkspacesDir},
		{"paths.views_dir", p.ViewsDir},
		{"paths.calendars_dir", p.CalendarsDir},
		{"paths.admin_logs_dir", p.AdminLogsDir},
		{"paths.tools_dir", p.ToolsDir},
		{"paths.executable", p.Executable},
		{"the default project's wiki", p.WikiDir},
		{"paths.base_config", p.BaseConfig},
	}
	for _, file := range p.ConfigFilesUsed {
		paths = append(paths, namedPath{"config.yaml", file})
	}
	if p.ConfigFileUsed != "" {
		paths = append(paths, namedPath{"config.yaml", p.ConfigFileUsed})
	}
	return paths
}

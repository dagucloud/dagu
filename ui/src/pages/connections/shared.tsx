// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  ConnectionProviderName,
  ConnectionStatus,
  UserRole,
  type components,
} from '@/api/v1/schema';
import { EmptyState } from '@/components/PageStates';
import { Badge } from '@/components/ui/badge';
import { CopyButton } from '@/components/ui/copy-button';
import { useAuth } from '@/contexts/AuthContext';
import { useConfig } from '@/contexts/ConfigContext';
import { useI18n } from '@/i18n/I18nProvider';
import { WorkspaceKind, sanitizeWorkspaceSelection } from '@/lib/workspace';
import type { WorkspaceSelection } from '@/lib/workspace';
import { effectiveWorkspaceRole, roleAtLeast } from '@/lib/workspaceAccess';
import { Plug } from 'lucide-react';
import React from 'react';

export type Connection = components['schemas']['Connection'];

/** The workspace value that names the connections of DAGs without one. */
export const GLOBAL_WORKSPACE = 'global';

/** The connections API serves only the server the browser talks to. */
export const LOCAL_NODE = 'local';

export const PROVIDER_LABELS: Record<ConnectionProviderName, string> = {
  [ConnectionProviderName.google]: 'Google Workspace',
  [ConnectionProviderName.microsoft]: 'Microsoft 365',
  [ConnectionProviderName.oauth2]: 'OAuth2',
  [ConnectionProviderName.mcp]: 'MCP server',
};

const STATUS_BADGES: Record<
  ConnectionStatus,
  { label: string; variant: 'success' | 'warning' | 'error' | 'default' }
> = {
  [ConnectionStatus.connected]: { label: 'Connected', variant: 'success' },
  [ConnectionStatus.pending]: { label: 'Not connected', variant: 'default' },
  [ConnectionStatus.needs_reconnect]: {
    label: 'Needs reconnect',
    variant: 'warning',
  },
  [ConnectionStatus.broken]: { label: 'Broken', variant: 'error' },
};

export function workspaceForSelection(selection?: WorkspaceSelection): string {
  const sanitized = sanitizeWorkspaceSelection(selection);
  if (sanitized.kind === WorkspaceKind.workspace && sanitized.workspace) {
    return sanitized.workspace;
  }
  return GLOBAL_WORKSPACE;
}

export function connectionsPath(workspace: string): string {
  return `/connections?workspace=${encodeURIComponent(workspace)}`;
}

/** The page of one connection, as connection events link to it. */
export function connectionPath(name: string, workspace: string): string {
  return `/connections/${encodeURIComponent(name)}?workspace=${encodeURIComponent(workspace)}`;
}

/**
 * What the current user may do with the connections of a workspace:
 * test them and list their tools as developer, and change, connect, and
 * remove them as manager.
 */
export function useConnectionAccess(workspace: string): {
  canTest: boolean;
  canManage: boolean;
} {
  const { user } = useAuth();
  const config = useConfig();
  if (config.authMode !== 'builtin') {
    return { canTest: true, canManage: true };
  }
  const role = effectiveWorkspaceRole(
    user,
    workspace === GLOBAL_WORKSPACE ? '' : workspace
  );
  return {
    canTest: roleAtLeast(role, UserRole.developer),
    canManage: roleAtLeast(role, UserRole.manager),
  };
}

export function ConnectionStatusBadge({
  status,
}: {
  status: ConnectionStatus;
}): React.ReactElement {
  const { ts } = useI18n();
  const badge = STATUS_BADGES[status];
  return <Badge variant={badge.variant}>{ts(badge.label)}</Badge>;
}

/** A value to register or hand to an admin, with a copy button. */
export function CopyableValue({
  label,
  value,
}: {
  label: string;
  value: string;
}): React.ReactElement {
  const { ts } = useI18n();
  return (
    <div className="space-y-1">
      <div className="text-xs font-medium text-muted-foreground">{label}</div>
      <div className="flex min-w-0 items-center gap-1 rounded-md bg-muted px-2 py-1">
        <code className="min-w-0 flex-1 whitespace-normal break-all text-xs">
          {value}
        </code>
        <CopyButton
          text={value}
          size="sm"
          label={ts('Copy {name}', { name: label })}
        />
      </div>
    </div>
  );
}

/** Shown in place of the pages while a remote node is selected. */
export function RemoteNodeNotice(): React.ReactElement {
  const { ts } = useI18n();
  return (
    <EmptyState
      headingLevel={1}
      icon={<Plug />}
      title={ts('Connections are kept on each server')}
      description={ts(
        'Remote nodes do not serve their connections. Select the local server to see its connections.'
      )}
    />
  );
}

// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useEffect, useMemo, type ReactNode } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { RuntimeProfileStatus } from '@/api/v1/schema';
import { Badge } from '@/components/ui/badge';
import { useCanManageProfiles, useIsAdmin } from '@/contexts/AuthContext';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import DAGSettingsTab from '@/features/dags/components/dag-details/DAGSettingsTab';
import { useProfiles } from '@/features/dags/hooks/useProfiles';
import { useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';
import { useI18n } from '@/i18n/I18nProvider';
import { formatAbsoluteTime } from '@/lib/dagRunTiming';
import { useSettingsLeaveGuard } from '../settings/useSettingsLeaveGuard';
import { DotenvBlock } from './DotenvBlock';
import {
  mergeProfileLayers,
  selectProfile,
  type ProfileLayerKind,
} from './runtimeProfile';

const LAYER_LABEL: Record<ProfileLayerKind, string> = {
  global: 'Global environment',
  workspace: 'Workspace defaults',
  profile: 'Profile',
};

function Fact({
  label,
  testId,
  children,
}: {
  label: string;
  testId: string;
  children: ReactNode;
}) {
  return (
    <div className="flex min-w-0 gap-2 text-xs">
      <dt className="w-32 shrink-0 text-muted-foreground">{label}</dt>
      <dd data-testid={testId} className="min-w-0 break-words text-foreground">
        {children}
      </dd>
    </div>
  );
}

/**
 * The runtime profile of the workflow, in the Overview tab of the Workflow
 * panel: the profile that a run uses, where the choice comes from, the
 * variables that it sets as one .env block for each layer (secret values
 * masked, the first lines shown, the rest behind a button),
 * and the control that changes the default profile. An unsaved choice
 * counts as a draft of the leave guard. The global and workspace layers
 * show when the role can read them.
 */
export function RuntimeProfileSection({
  fileName,
  workspaceName,
}: {
  fileName: string;
  workspaceName: string | null;
}) {
  const { ts } = useI18n();
  const navigate = useNavigate();
  const remoteNode = useRemoteNode();
  const canManage = useCanManageProfiles();
  const isAdmin = useIsAdmin();
  const { setDirty, guardLeave } = useSettingsLeaveGuard();

  const onDirtyChange = useCallback(
    (dirty: boolean) => setDirty('profile', dirty),
    [setDirty]
  );
  // The section goes away with its draft.
  useEffect(() => () => setDirty('profile', false), [setDirty]);

  // The same request as the control below: one shared cache entry.
  const settingsInit = useMemo(
    () =>
      whenEnabled(!!fileName, {
        params: { path: { fileName }, query: { remoteNode } },
      }),
    [fileName, remoteNode]
  );
  const { data: settings } = useQuery(
    '/dags/{fileName}/settings',
    settingsInit
  );

  const { profiles } = useProfiles({ enabled: canManage });
  const { data: globalLayer, error: globalError } = useQuery(
    '/profiles/_global',
    whenEnabled(isAdmin, { params: { query: { remoteNode } } })
  );
  const { data: workspaceLayer, error: workspaceError } = useQuery(
    '/profiles/_workspaces/{workspaceName}',
    workspaceName && canManage
      ? { params: { path: { workspaceName }, query: { remoteNode } } }
      : null
  );

  const selection = selectProfile(
    settings?.profile,
    workspaceLayer?.defaultProfile
  );
  const record = profiles.find((profile) => profile.name === selection.name);
  const disabled = record?.status === RuntimeProfileStatus.disabled;
  const globalReadable = !!globalLayer && !globalError;
  const workspaceReadable = !!workspaceLayer && !workspaceError;

  const rows = useMemo(
    () =>
      mergeProfileLayers([
        { kind: 'global', entries: globalReadable ? globalLayer.entries : [] },
        {
          kind: 'workspace',
          entries: workspaceReadable ? workspaceLayer.entries : [],
        },
        { kind: 'profile', entries: record?.entries ?? [] },
      ]),
    [globalLayer, globalReadable, record, workspaceLayer, workspaceReadable]
  );
  // One .env block for each layer, in the order of the merge; a key is in
  // the block of the layer that gives its value.
  const groups = useMemo(
    () =>
      (['global', 'workspace', 'profile'] as const)
        .map((layer) => ({
          layer,
          entries: rows.filter((row) => row.layer === layer),
        }))
        .filter((group) => group.entries.length > 0),
    [rows]
  );
  const secretCount = rows.filter((row) => row.kind === 'secret').length;

  const layerName = (layer: ProfileLayerKind) =>
    layer === 'profile' ? selection.name : ts(LAYER_LABEL[layer]);

  const openProfiles = (event: { preventDefault(): void }) => {
    event.preventDefault();
    guardLeave(() => navigate('/profiles'));
  };

  let appliesText: string;
  if (selection.source === 'workflow') {
    appliesText = ts('Yes: the default profile of this workflow');
  } else if (selection.source === 'workspace') {
    appliesText = ts('Yes: the default profile of the workspace {workspace}', {
      workspace: workspaceName ?? '',
    });
  } else {
    appliesText = ts('No profile applies to this workflow');
  }

  return (
    <section
      data-testid="ww-runtime-profile"
      aria-label={ts('Runtime profile')}
      className="space-y-3"
    >
      <div className="flex items-center justify-between gap-2">
        <h3 className="text-sm font-medium text-foreground">
          {ts('Runtime profile')}
        </h3>
        {canManage && (
          <Link
            to="/profiles"
            onClick={openProfiles}
            className="text-xs text-muted-foreground underline-offset-2 hover:text-foreground hover:underline"
          >
            {ts('Manage profiles')}
          </Link>
        )}
      </div>
      <p className="text-xs text-muted-foreground">
        {ts(
          'A profile sets environment variables for every run of this workflow. A run can pick another profile in the Start dialog.'
        )}
      </p>

      <dl className="space-y-1.5 rounded-md border border-border p-3">
        <Fact label={ts('Applies')} testId="ww-rp-applies">
          {appliesText}
        </Fact>
        <Fact label={ts('Profile')} testId="ww-rp-name">
          {selection.name ? (
            <span className="flex flex-wrap items-center gap-1.5">
              <span className="font-medium">{selection.name}</span>
              {record?.protected && (
                <Badge variant="outline" className="h-4 px-1.5 text-[10px]">
                  {ts('Protected')}
                </Badge>
              )}
              {disabled && (
                <Badge variant="secondary" className="h-4 px-1.5 text-[10px]">
                  {ts('Disabled')}
                </Badge>
              )}
            </span>
          ) : (
            ts('No profile')
          )}
        </Fact>
        {disabled && (
          <p role="alert" className="text-xs text-destructive">
            {ts('This profile is disabled. A run cannot use it.')}
          </p>
        )}
        {record?.description && (
          <Fact label={ts('Description')} testId="ww-rp-description">
            {record.description}
          </Fact>
        )}
        {selection.name && !record && canManage && (
          <p className="text-xs text-muted-foreground">
            {ts('The details of this profile are not available.')}
          </p>
        )}
        {record && (
          <>
            <Fact label={ts('Variables')} testId="ww-rp-counts">
              {ts('{variables} variables, {secrets} secrets', {
                variables: String(rows.length - secretCount),
                secrets: String(secretCount),
              })}
            </Fact>
            <Fact label={ts('Updated')} testId="ww-rp-updated">
              {formatAbsoluteTime(record.updatedAt, { seconds: false }) ?? '-'}
            </Fact>
          </>
        )}
      </dl>

      {rows.length > 0 && (
        <div
          data-testid="ww-rp-list"
          role="group"
          aria-label={ts('Variables')}
          className="space-y-2"
        >
          {groups.map((group) => (
            <DotenvBlock
              key={group.layer}
              testId={`ww-rp-group-${group.layer}`}
              title={layerName(group.layer)}
              entries={group.entries}
              layerName={layerName}
            />
          ))}
        </div>
      )}
      {(!isAdmin || !!globalError) && (
        <p className="text-xs text-muted-foreground">
          {ts('Global variables are visible to administrators only.')}
        </p>
      )}
      {!!workspaceName && (!canManage || !!workspaceError) && (
        <p className="text-xs text-muted-foreground">
          {ts('The defaults of the workspace are not available to your role.')}
        </p>
      )}

      <DAGSettingsTab fileName={fileName} onDirtyChange={onDirtyChange} />
    </section>
  );
}

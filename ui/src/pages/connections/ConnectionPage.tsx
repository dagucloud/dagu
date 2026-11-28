// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  ConnectionAccess,
  ConnectionProviderName,
  ConnectionStatus,
  type components,
} from '@/api/v1/schema';
import {
  LoadErrorState,
  NotFoundState,
  PageLoading,
} from '@/components/PageStates';
import { Button } from '@/components/ui/button';
import ConfirmModal from '@/components/ui/confirm-dialog';
import Title from '@/components/ui/title';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useClient, useQuery } from '@/hooks/api';
import { useI18n } from '@/i18n/I18nProvider';
import dayjs from '@/lib/dayjs';
import { isNotFoundQueryError } from '@/lib/queryErrors';
import { Loader2, PlugZap, RefreshCw, Trash2 } from 'lucide-react';
import React, { useContext, useEffect, useState } from 'react';
import {
  useLocation,
  useNavigate,
  useParams,
  useSearchParams,
} from 'react-router-dom';
import { ConnectDialog, ProbeResults, ScopeList } from './ConnectDialog';
import { ConnectionTools } from './ConnectionTools';
import {
  type Connection,
  type ConnectionPageState,
  ConnectionStatusBadge,
  GLOBAL_WORKSPACE,
  LOCAL_NODE,
  PROVIDER_LABELS,
  RemoteNodeNotice,
  connectionsPath,
  useConnectionAccess,
} from './shared';

type ConnectionProbe = components['schemas']['ConnectionProbe'];

/**
 * Shows one connection: its status and why, its account, services, and
 * granted scopes, and the tools of its MCP services. Managers reconnect and
 * remove it; developers test it.
 */
export default function ConnectionPage(): React.ReactElement {
  const appBarContext = useContext(AppBarContext);
  const { t } = useI18n();
  const remote =
    !!appBarContext.selectedRemoteNode &&
    appBarContext.selectedRemoteNode !== LOCAL_NODE;

  useEffect(() => {
    appBarContext.setTitle(t('navigation.connections'));
  }, [appBarContext, t]);

  if (remote) return <RemoteNodeNotice />;
  return <ConnectionDetails />;
}

function ConnectionDetails(): React.ReactElement {
  const { name = '' } = useParams();
  const [searchParams] = useSearchParams();
  const workspace = searchParams.get('workspace') || GLOBAL_WORKSPACE;
  const { ts } = useI18n();
  const { data, error, isLoading, mutate } = useQuery('/connections/{name}', {
    params: {
      path: { name },
      query: { workspace, remoteNode: LOCAL_NODE },
    },
  });

  if (isLoading) return <PageLoading />;
  if (error && !data) {
    if (isNotFoundQueryError(error)) {
      return (
        <NotFoundState
          title={ts('Connection not found')}
          description={ts('No connection {name} in workspace {workspace}.', {
            name,
            workspace,
          })}
          action={{
            to: connectionsPath(workspace),
            label: ts('Back to connections'),
          }}
        />
      );
    }
    return (
      <LoadErrorState
        title={ts("Couldn't load the connection.")}
        error={error}
        onRetry={() => void mutate()}
      />
    );
  }
  if (!data) return <PageLoading />;
  return <ConnectionView connection={data} onChanged={() => void mutate()} />;
}

function ConnectionView({
  connection,
  onChanged,
}: {
  connection: Connection;
  onChanged: () => void;
}): React.ReactElement {
  const client = useClient();
  const navigate = useNavigate();
  const { ts } = useI18n();
  const { canTest, canManage } = useConnectionAccess(connection.workspace);
  const location = useLocation();
  // The wizard opens a new connection's page to sign in at once.
  const [connecting, setConnecting] = useState(
    () => (location.state as ConnectionPageState | null)?.connect === true
  );
  useEffect(() => {
    if ((location.state as ConnectionPageState | null)?.connect) {
      // A reload of the page does not start signing in again.
      navigate(`${location.pathname}${location.search}`, {
        replace: true,
        state: null,
      });
    }
  }, [location, navigate]);
  const [removing, setRemoving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [probes, setProbes] = useState<ConnectionProbe[] | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  // Advances on each confirmed reconnect, which may sign in another account,
  // so the tools panel lists that account's tools.
  const [reconnects, setReconnects] = useState(0);
  const params = {
    path: { name: connection.name },
    query: { workspace: connection.workspace, remoteNode: LOCAL_NODE },
  };
  const connected = connection.status === ConnectionStatus.connected;
  const broken = connection.status === ConnectionStatus.broken;

  async function test() {
    setTesting(true);
    setActionError(null);
    setProbes(null);
    try {
      const { data, error } = await client.POST('/connections/{name}/test', {
        params,
      });
      if (error || !data) {
        throw new Error(error?.message || ts('The test failed.'));
      }
      setProbes(data.services);
    } catch (err) {
      setActionError(
        err instanceof Error ? err.message : ts('The test failed.')
      );
    } finally {
      setTesting(false);
    }
  }

  async function remove() {
    setActionError(null);
    const failed = ts('Could not remove the connection.');
    try {
      const { error } = await client.DELETE('/connections/{name}', { params });
      if (error) throw new Error(error.message || failed);
    } catch (err) {
      setRemoving(false);
      setActionError(
        err instanceof Error && err.message ? err.message : failed
      );
      return;
    }
    navigate(connectionsPath(connection.workspace));
  }

  return (
    <div className="flex h-full min-h-0 flex-col gap-4 overflow-auto">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex min-w-0 items-center gap-2">
          <Title className="truncate">{connection.name}</Title>
          <ConnectionStatusBadge status={connection.status} />
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {canManage && !broken && (
            <Button
              size="sm"
              variant="primary"
              onClick={() => setConnecting(true)}
            >
              <PlugZap className="h-4 w-4" />
              {connection.status === ConnectionStatus.pending
                ? ts('Connect')
                : ts('Reconnect')}
            </Button>
          )}
          {canTest && connected && (
            <Button size="sm" disabled={testing} onClick={() => void test()}>
              {testing ? (
                <Loader2 className="h-4 w-4 animate-spin" />
              ) : (
                <RefreshCw className="h-4 w-4" />
              )}
              {ts('Test')}
            </Button>
          )}
          {canManage && (
            <Button size="sm" onClick={() => setRemoving(true)}>
              <Trash2 className="h-4 w-4" />
              {ts('Remove')}
            </Button>
          )}
        </div>
      </div>

      {connection.statusReason && (
        <p
          role="status"
          className="whitespace-normal break-words rounded-md bg-warning/10 p-3 text-sm"
        >
          {connection.statusReason}
        </p>
      )}
      {actionError && (
        <p
          role="alert"
          className="rounded-md bg-destructive/10 p-3 text-sm text-destructive"
        >
          {actionError}
        </p>
      )}
      {probes && (
        <section className="card-obsidian p-3 text-sm">
          <h2 className="mb-2 text-sm font-semibold">{ts('Test results')}</h2>
          <ProbeResults probes={probes} />
        </section>
      )}

      <section className="card-obsidian p-3">
        <dl className="grid gap-x-6 gap-y-2 text-sm sm:grid-cols-[max-content_1fr]">
          <Detail label={ts('Provider')}>
            {PROVIDER_LABELS[connection.provider]}
          </Detail>
          <Detail label={ts('Account')}>
            {connection.account || (
              <span className="text-muted-foreground">{ts('None')}</span>
            )}
          </Detail>
          <Detail label={ts('Workspace')}>
            {connection.workspace === GLOBAL_WORKSPACE
              ? ts('Global')
              : connection.workspace}
          </Detail>
          {connection.refreshedAt && (
            <Detail label={ts('Last refreshed')}>
              {dayjs(connection.refreshedAt).format('MMM D, YYYY HH:mm')}
            </Detail>
          )}
          {connection.refreshTokenExpiresAt && (
            <Detail label={ts('Sign-in ends')}>
              {dayjs(connection.refreshTokenExpiresAt).format(
                'MMM D, YYYY HH:mm'
              )}
            </Detail>
          )}
        </dl>
      </section>

      <section className="card-obsidian p-3 text-sm">
        <h2 className="mb-2 text-sm font-semibold">{ts('Services')}</h2>
        <ul className="space-y-1.5">
          {connection.services.map((service) => (
            <li key={service.name}>
              <span className="font-medium">{service.name}</span>
              <span className="text-muted-foreground">
                {' · '}
                {service.access === ConnectionAccess.write
                  ? ts('Read and write')
                  : ts('Read')}
              </span>
              {service.missingScopes.length > 0 && (
                <div className="mt-1 space-y-1 rounded-md bg-warning/10 p-2">
                  <p>
                    {ts(
                      'Not granted. Reconnect to ask for the missing scopes:'
                    )}
                  </p>
                  <ScopeList scopes={service.missingScopes} />
                </div>
              )}
            </li>
          ))}
        </ul>
        {connection.grantedScopes.length > 0 && (
          <div className="mt-3 space-y-1">
            <h3 className="text-xs font-medium text-muted-foreground">
              {ts('Granted scopes')}
            </h3>
            <ScopeList scopes={connection.grantedScopes} />
          </div>
        )}
      </section>

      {canTest && connected && (
        <section className="card-obsidian p-3">
          <h2 className="mb-2 text-sm font-semibold">{ts('Tools')}</h2>
          <ConnectionTools
            key={reconnects}
            name={connection.name}
            workspace={connection.workspace}
          />
        </section>
      )}

      {canManage && !broken && (
        <ConnectDialog
          connection={connection}
          open={connecting}
          onClose={() => setConnecting(false)}
          onConnected={() => {
            setReconnects((count) => count + 1);
            onChanged();
          }}
        />
      )}

      <ConfirmModal
        title={ts('Remove connection')}
        buttonText={ts('Remove')}
        visible={removing}
        dismissModal={() => setRemoving(false)}
        onSubmit={() => void remove()}
        initialFocus="cancel"
      >
        <div className="space-y-2 text-sm text-muted-foreground">
          <p>
            {ts(
              'Remove {name}? Its tokens and tool lists are deleted, and DAGs that name it fail when they start.',
              { name: connection.name }
            )}
          </p>
          <p>
            {connection.provider === ConnectionProviderName.google
              ? ts(
                  'Dagu also revokes its sign-in at Google, so connections on other servers that use a client of the same Google Cloud project may lose access.'
                )
              : ts(
                  'Dagu also revokes its sign-in at the provider, so connections on other servers that use the same client may lose access.'
                )}
          </p>
        </div>
      </ConfirmModal>
    </div>
  );
}

function Detail({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}): React.ReactElement {
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="min-w-0 whitespace-normal break-words">{children}</dd>
    </>
  );
}

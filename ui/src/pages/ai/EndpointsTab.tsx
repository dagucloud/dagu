// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { KeyRound, Pencil, Plus, RefreshCw, Trash2 } from 'lucide-react';
import {
  useCallback,
  useContext,
  useRef,
  useState,
  type ReactElement,
} from 'react';
import {
  EmptyState,
  LoadErrorState,
  PageLoading,
} from '@/components/PageStates';
import { Button } from '@/components/ui/button';
import ConfirmDialog from '@/components/ui/confirm-dialog';
import { IconButton } from '@/components/ui/icon-button';
import { RelativeTime } from '@/components/ui/relative-time';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { AppBarContext } from '@/contexts/AppBarContext';
import {
  allowSummary,
  describeApiError,
  findPreset,
  keyText,
  scopeQuery,
  scopeWorkspace,
  type AIEndpoint,
  type AIEndpointPreset,
} from '@/features/ai-endpoints/endpoint';
import { providerIcon } from '@/features/visual-editor/canvas/providerLogo';
import { useClient, useQuery } from '@/hooks/api';
import { useI18n } from '@/i18n/I18nProvider';
import { EndpointDialog } from './EndpointDialog';
import { KeyDialog } from './KeyDialog';
import { ModelsCell } from './ModelsCell';
import { ModelsDialog } from './ModelsDialog';

type Dialogs =
  | { kind: 'add' }
  | { kind: 'edit'; endpoint: AIEndpoint }
  | { kind: 'key'; endpoint: AIEndpoint }
  | { kind: 'models'; endpoint: AIEndpoint }
  | { kind: 'delete'; endpoint: AIEndpoint };

/** How often the list is read again while a first read of a models list runs. */
const PENDING_POLL_MS = 2500;
/** The number of polls after a change. A read that lasts longer shows on the next visit. */
const PENDING_POLL_BUDGET = 12;

function endpointId(endpoint: AIEndpoint): string {
  return `${endpoint.scope}/${endpoint.name}`;
}

/** True while the first read of the list of an endpoint has no result: no date and no error. */
function hasPendingRead(endpoints: readonly AIEndpoint[] | undefined): boolean {
  return (endpoints ?? []).some(
    (endpoint) =>
      endpoint.models?.supported === true &&
      !endpoint.models.refreshedAt &&
      !endpoint.models.error
  );
}

/**
 * The AI endpoints of the server. An endpoint holds the
 * address and the key of a model API. The list shows the first characters
 * and the fingerprint of a key, never the key.
 */
export function EndpointsTab(): ReactElement {
  const { ts } = useI18n();
  const client = useClient();
  const appBar = useContext(AppBarContext);
  const remoteNode = appBar.selectedRemoteNode || 'local';
  const [dialog, setDialog] = useState<Dialogs | null>(null);
  const [deleteBusy, setDeleteBusy] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);
  const [reloading, setReloading] = useState<ReadonlySet<string>>(new Set());
  const [reloadErrors, setReloadErrors] = useState<Record<string, string>>({});
  // The server reads the models of a new endpoint after it saves it. The list
  // asks again for a short time, until the read has a result.
  const pollBudget = useRef(PENDING_POLL_BUDGET);

  const list = useQuery(
    '/ai/endpoints',
    { params: { query: { remoteNode } } },
    {
      revalidateOnMount: true,
      revalidateOnFocus: true,
      refreshInterval: (latest?: { endpoints?: AIEndpoint[] }) =>
        hasPendingRead(latest?.endpoints) && pollBudget.current-- > 0
          ? PENDING_POLL_MS
          : 0,
    }
  );
  const presetsQuery = useQuery('/ai/endpoint-presets', {
    params: { query: { remoteNode } },
  });
  const endpoints = list.data?.endpoints;
  const presets = presetsQuery.data?.presets;

  const saved = useCallback(() => {
    setDialog(null);
    pollBudget.current = PENDING_POLL_BUDGET;
    void list.mutate();
  }, [list]);

  const reload = async (endpoint: AIEndpoint) => {
    const id = endpointId(endpoint);
    setReloading((current) => new Set(current).add(id));
    setReloadErrors((current) => {
      const next = { ...current };
      delete next[id];
      return next;
    });
    try {
      const result = await client.POST('/ai/endpoints/{name}/models/reload', {
        params: {
          path: { name: endpoint.name },
          query: { remoteNode, workspace: scopeQuery(endpoint.scope) },
        },
      });
      if (result.error) {
        const text = describeApiError(
          ts,
          result.response.status,
          result.error,
          ts('The models were not reloaded.')
        );
        setReloadErrors((current) => ({ ...current, [id]: text }));
      }
      await list.mutate();
    } catch {
      setReloadErrors((current) => ({
        ...current,
        [id]: ts('The server did not respond.'),
      }));
    } finally {
      setReloading((current) => {
        const next = new Set(current);
        next.delete(id);
        return next;
      });
    }
  };

  const remove = async (endpoint: AIEndpoint) => {
    setDeleteBusy(true);
    setDeleteError(null);
    try {
      const result = await client.DELETE('/ai/endpoints/{name}', {
        params: {
          path: { name: endpoint.name },
          query: { remoteNode, workspace: scopeQuery(endpoint.scope) },
        },
      });
      if (result.error) {
        setDeleteError(
          describeApiError(
            ts,
            result.response.status,
            result.error,
            ts('The endpoint was not deleted.')
          )
        );
        return;
      }
      setDialog(null);
      await list.mutate();
    } catch {
      setDeleteError(ts('The server did not respond.'));
    } finally {
      setDeleteBusy(false);
    }
  };

  const addButton = (
    <Button
      variant="primary"
      size="sm"
      className="h-8"
      disabled={!presets}
      onClick={() => setDialog({ kind: 'add' })}
    >
      <Plus className="h-4 w-4" />
      {ts('Add endpoint')}
    </Button>
  );

  let body: ReactElement;
  if (!endpoints && list.error) {
    body = (
      <LoadErrorState
        title={ts("Couldn't load the endpoints")}
        error={list.error}
        onRetry={() => void list.mutate()}
      />
    );
  } else if (!endpoints) {
    body = <PageLoading />;
  } else if (endpoints.length === 0) {
    body = (
      <EmptyState
        icon={<KeyRound />}
        title={ts('No endpoints yet')}
        description={ts(
          'An endpoint holds the address and the key of a model API. A step names it, and the server keeps the key.'
        )}
        action={addButton}
      />
    );
  } else {
    const summaryText = {
      all: ts('All workspaces'),
      allDags: ts('All DAGs'),
      dags: (count: number) => ts('{count} DAGs', { count }),
      workspaces: (count: number) => ts('{count} workspaces', { count }),
      join: (parts: string[]) => parts.join(' · '),
    };
    body = (
      <Table className="text-xs">
        <TableHeader>
          <TableRow>
            <TableHead>{ts('Name')}</TableHead>
            <TableHead>{ts('Preset')}</TableHead>
            <TableHead>{ts('Scope')}</TableHead>
            <TableHead>{ts('Key')}</TableHead>
            <TableHead>{ts('Key updated')}</TableHead>
            <TableHead>{ts('Allowed')}</TableHead>
            <TableHead>{ts('Models')}</TableHead>
            <TableHead className="w-[110px]">
              <span className="sr-only">{ts('Actions')}</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {endpoints.map((endpoint) => (
            <EndpointRow
              key={endpointId(endpoint)}
              endpoint={endpoint}
              reloading={reloading.has(endpointId(endpoint))}
              reloadError={reloadErrors[endpointId(endpoint)] ?? null}
              onShowModels={() => setDialog({ kind: 'models', endpoint })}
              onReload={() => void reload(endpoint)}
              preset={
                presets ? findPreset(presets, endpoint.preset) : undefined
              }
              allowText={allowSummary(
                endpoint.allow,
                endpoint.scope,
                summaryText
              )}
              onEdit={() => setDialog({ kind: 'edit', endpoint })}
              onKey={() => setDialog({ kind: 'key', endpoint })}
              onDelete={() => {
                setDeleteError(null);
                setDialog({ kind: 'delete', endpoint });
              }}
            />
          ))}
        </TableBody>
      </Table>
    );
  }

  return (
    <div className="flex flex-col gap-4">
      {endpoints && endpoints.length > 0 && (
        <div className="flex min-h-9 flex-wrap items-center justify-between gap-3">
          <p className="text-sm text-muted-foreground">
            {ts(
              'An endpoint holds the address and the key of a model API. A step names it, and the server keeps the key.'
            )}
          </p>
          {addButton}
        </div>
      )}
      {presetsQuery.error && !presets && (
        <p role="alert" className="text-sm text-destructive">
          {ts("Couldn't load the presets of the endpoints.")}
        </p>
      )}
      {body}
      {dialog?.kind === 'add' && presets && (
        <EndpointDialog
          presets={presets}
          endpoints={endpoints ?? []}
          onSaved={saved}
          onClose={() => setDialog(null)}
        />
      )}
      {dialog?.kind === 'edit' && presets && (
        <EndpointDialog
          endpoint={dialog.endpoint}
          presets={presets}
          endpoints={endpoints ?? []}
          onSaved={saved}
          onClose={() => setDialog(null)}
        />
      )}
      {dialog?.kind === 'key' && (
        <KeyDialog
          endpoint={dialog.endpoint}
          onSaved={saved}
          onClose={() => setDialog(null)}
        />
      )}
      {dialog?.kind === 'models' && (
        <ModelsDialog
          endpoint={dialog.endpoint}
          onClose={() => setDialog(null)}
        />
      )}
      <ConfirmDialog
        title={ts('Delete endpoint?')}
        buttonText={ts('Delete')}
        visible={dialog?.kind === 'delete'}
        dismissModal={() => !deleteBusy && setDialog(null)}
        onSubmit={() => {
          if (dialog?.kind === 'delete') void remove(dialog.endpoint);
        }}
        submitDisabled={deleteBusy}
        initialFocus="cancel"
      >
        <p className="text-sm">
          {ts(
            'Delete "{name}"? A step that names this endpoint fails until you create it again. The key is deleted too.',
            {
              name: dialog?.kind === 'delete' ? dialog.endpoint.name : '',
            }
          )}
        </p>
        {deleteError && (
          <p role="alert" className="mt-2 text-sm text-destructive">
            {deleteError}
          </p>
        )}
      </ConfirmDialog>
    </div>
  );
}

function EndpointRow({
  endpoint,
  preset,
  allowText,
  reloading,
  reloadError,
  onShowModels,
  onReload,
  onEdit,
  onKey,
  onDelete,
}: {
  endpoint: AIEndpoint;
  preset: AIEndpointPreset | undefined;
  allowText: string;
  reloading: boolean;
  reloadError: string | null;
  onShowModels(): void;
  onReload(): void;
  onEdit(): void;
  onKey(): void;
  onDelete(): void;
}): ReactElement {
  const { ts } = useI18n();
  const Icon = providerIcon(endpoint.preset);
  const workspace = scopeWorkspace(endpoint.scope);
  return (
    <TableRow>
      <TableCell className="whitespace-normal">
        <div className="font-mono font-medium">{endpoint.name}</div>
        {endpoint.description && (
          <div className="text-muted-foreground">{endpoint.description}</div>
        )}
      </TableCell>
      <TableCell>
        <span className="inline-flex items-center gap-1.5">
          <Icon className="size-3.5 shrink-0" aria-hidden="true" />
          <span>{preset?.label ?? endpoint.preset}</span>
          <span className="text-muted-foreground">{endpoint.protocol}</span>
        </span>
      </TableCell>
      <TableCell>
        {workspace === null
          ? ts('Global')
          : ts('Workspace {name}', { name: workspace })}
      </TableCell>
      <TableCell className="font-mono">
        {endpoint.keyFingerprint ? (
          <span>{keyText(endpoint.keyPrefix, endpoint.keyFingerprint)}</span>
        ) : (
          <span className="font-sans text-muted-foreground">
            {ts('No key')}
          </span>
        )}
      </TableCell>
      <TableCell className="text-muted-foreground">
        {endpoint.keyUpdatedAt ? (
          <RelativeTime timestamp={endpoint.keyUpdatedAt} />
        ) : (
          '-'
        )}
      </TableCell>
      <TableCell className="whitespace-normal">{allowText}</TableCell>
      <TableCell className="min-w-[200px] whitespace-normal">
        <ModelsCell
          endpoint={endpoint}
          busy={reloading}
          reloadError={reloadError}
          onShow={onShowModels}
          onReload={onReload}
        />
      </TableCell>
      <TableCell>
        <div className="flex items-center justify-end gap-0.5">
          <IconButton
            size="sm"
            label={ts('Edit {name}', { name: endpoint.name })}
            icon={Pencil}
            onClick={onEdit}
          />
          <IconButton
            size="sm"
            label={ts('Rotate the key of {name}', { name: endpoint.name })}
            icon={RefreshCw}
            onClick={onKey}
          />
          <IconButton
            size="sm"
            tone="destructive"
            label={ts('Delete endpoint {name}', { name: endpoint.name })}
            icon={Trash2}
            onClick={onDelete}
          />
        </div>
      </TableCell>
    </TableRow>
  );
}

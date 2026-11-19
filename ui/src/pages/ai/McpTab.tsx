// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Pencil, Plug, Plus, RefreshCw, Trash2 } from 'lucide-react';
import { useCallback, useContext, useState, type ReactElement } from 'react';
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
  keyText,
  scopeQuery,
  scopeWorkspace,
} from '@/features/ai-endpoints/endpoint';
import {
  credentialsOf,
  credentialsUpdatedAt,
  describeMcpApiError,
  urlHost,
  type AIMCPServer,
} from '@/features/ai-endpoints/mcpServer';
import { useClient, useQuery } from '@/hooks/api';
import { useI18n } from '@/i18n/I18nProvider';
import { McpCredentialsDialog } from './McpCredentialsDialog';
import { McpServerDialog } from './McpServerDialog';

type Dialogs =
  | { kind: 'add' }
  | { kind: 'edit'; server: AIMCPServer }
  | { kind: 'credentials'; server: AIMCPServer }
  | { kind: 'delete'; server: AIMCPServer };

function serverId(server: AIMCPServer): string {
  return `${server.scope}/${server.name}`;
}

/**
 * The MCP servers of the server: the entries that hold a URL and credentials. A
 * step names an entry, and the coordinator calls the MCP server with the
 * credentials. The list shows the first characters and the fingerprint of each
 * credential, never the value.
 */
export function McpTab(): ReactElement {
  const { ts } = useI18n();
  const client = useClient();
  const appBar = useContext(AppBarContext);
  const remoteNode = appBar.selectedRemoteNode || 'local';
  const [dialog, setDialog] = useState<Dialogs | null>(null);
  const [deleteBusy, setDeleteBusy] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  const list = useQuery(
    '/ai/mcp-servers',
    { params: { query: { remoteNode } } },
    { revalidateOnMount: true, revalidateOnFocus: true }
  );
  const servers = list.data?.servers;

  const saved = useCallback(() => {
    setDialog(null);
    void list.mutate();
  }, [list]);

  const remove = async (server: AIMCPServer) => {
    setDeleteBusy(true);
    setDeleteError(null);
    try {
      const result = await client.DELETE('/ai/mcp-servers/{name}', {
        params: {
          path: { name: server.name },
          query: { remoteNode, workspace: scopeQuery(server.scope) },
        },
      });
      if (result.error) {
        setDeleteError(
          describeMcpApiError(
            ts,
            result.response.status,
            result.error,
            ts('The MCP server was not deleted.')
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
      onClick={() => setDialog({ kind: 'add' })}
    >
      <Plus className="h-4 w-4" />
      {ts('Add MCP server')}
    </Button>
  );
  const intro = ts(
    'A MCP server entry holds a URL and credentials. A step names it, and the coordinator keeps the credentials.'
  );

  let body: ReactElement;
  if (!servers && list.error) {
    body = (
      <LoadErrorState
        title={ts("Couldn't load the MCP servers")}
        error={list.error}
        onRetry={() => void list.mutate()}
      />
    );
  } else if (!servers) {
    body = <PageLoading />;
  } else if (servers.length === 0) {
    body = (
      <EmptyState
        icon={<Plug />}
        title={ts('No MCP servers yet')}
        description={intro}
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
            <TableHead>{ts('Server')}</TableHead>
            <TableHead>{ts('Scope')}</TableHead>
            <TableHead>{ts('Credentials')}</TableHead>
            <TableHead>{ts('Updated')}</TableHead>
            <TableHead>{ts('Allowed')}</TableHead>
            <TableHead className="w-[110px]">
              <span className="sr-only">{ts('Actions')}</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {servers.map((server) => (
            <McpRow
              key={serverId(server)}
              server={server}
              allowText={allowSummary(server.allow, server.scope, summaryText)}
              onEdit={() => setDialog({ kind: 'edit', server })}
              onCredentials={() => setDialog({ kind: 'credentials', server })}
              onDelete={() => {
                setDeleteError(null);
                setDialog({ kind: 'delete', server });
              }}
            />
          ))}
        </TableBody>
      </Table>
    );
  }

  return (
    <div className="flex flex-col gap-4">
      {servers && servers.length > 0 && (
        <div className="flex min-h-9 flex-wrap items-center justify-between gap-3">
          <p className="text-sm text-muted-foreground">{intro}</p>
          {addButton}
        </div>
      )}
      {body}
      {dialog?.kind === 'add' && (
        <McpServerDialog
          servers={servers ?? []}
          onSaved={saved}
          onClose={() => setDialog(null)}
        />
      )}
      {dialog?.kind === 'edit' && (
        <McpServerDialog
          server={dialog.server}
          servers={servers ?? []}
          onSaved={saved}
          onClose={() => setDialog(null)}
        />
      )}
      {dialog?.kind === 'credentials' && (
        <McpCredentialsDialog
          server={dialog.server}
          onSaved={saved}
          onClose={() => setDialog(null)}
        />
      )}
      <ConfirmDialog
        title={ts('Delete MCP server?')}
        buttonText={ts('Delete')}
        visible={dialog?.kind === 'delete'}
        dismissModal={() => !deleteBusy && setDialog(null)}
        onSubmit={() => {
          if (dialog?.kind === 'delete') void remove(dialog.server);
        }}
        submitDisabled={deleteBusy}
        initialFocus="cancel"
      >
        <p className="text-sm">
          {ts(
            'Delete "{name}"? A step that names this MCP server fails until you create it again. The credentials are deleted too.',
            { name: dialog?.kind === 'delete' ? dialog.server.name : '' }
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

function McpRow({
  server,
  allowText,
  onEdit,
  onCredentials,
  onDelete,
}: {
  server: AIMCPServer;
  allowText: string;
  onEdit(): void;
  onCredentials(): void;
  onDelete(): void;
}): ReactElement {
  const { ts } = useI18n();
  const workspace = scopeWorkspace(server.scope);
  const credentials = credentialsOf(server);
  const updated = credentialsUpdatedAt(server);
  return (
    <TableRow>
      <TableCell className="font-mono font-medium">{server.name}</TableCell>
      <TableCell className="font-mono">{urlHost(server.url)}</TableCell>
      <TableCell>
        {workspace === null
          ? ts('Global')
          : ts('Workspace {name}', { name: workspace })}
      </TableCell>
      <TableCell className="whitespace-normal font-mono">
        {credentials.length === 0 ? (
          <span className="font-sans text-muted-foreground">
            {ts('No credential')}
          </span>
        ) : (
          <ul className="space-y-0.5">
            {credentials.map((credential) => (
              <li key={credential.label}>
                <span className="font-sans text-muted-foreground">
                  {credential.label}{' '}
                </span>
                {keyText(credential.prefix, credential.fingerprint)}
              </li>
            ))}
          </ul>
        )}
      </TableCell>
      <TableCell className="text-muted-foreground">
        {updated ? <RelativeTime timestamp={updated} /> : '-'}
      </TableCell>
      <TableCell className="whitespace-normal">{allowText}</TableCell>
      <TableCell>
        <div className="flex items-center justify-end gap-0.5">
          <IconButton
            size="sm"
            label={ts('Edit {name}', { name: server.name })}
            icon={Pencil}
            onClick={onEdit}
          />
          <IconButton
            size="sm"
            label={ts('Rotate the credentials of {name}', {
              name: server.name,
            })}
            icon={RefreshCw}
            onClick={onCredentials}
          />
          <IconButton
            size="sm"
            tone="destructive"
            label={ts('Delete MCP server {name}', { name: server.name })}
            icon={Trash2}
            onClick={onDelete}
          />
        </div>
      </TableCell>
    </TableRow>
  );
}

// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useContext, useState, type ReactElement } from 'react';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { AppBarContext } from '@/contexts/AppBarContext';
import { scopeQuery } from '@/features/ai-endpoints/endpoint';
import {
  describeMcpApiError,
  type AIMCPServer,
  type CredentialMode,
} from '@/features/ai-endpoints/mcpServer';
import { useClient } from '@/hooks/api';
import { useI18n } from '@/i18n/I18nProvider';
import { CredentialFields, useCredentialFields } from './CredentialFields';

/** The form of the credentials that an entry has now. */
function currentMode(server: AIMCPServer): CredentialMode {
  if (server.token) return 'token';
  return (server.headers ?? []).length > 0 ? 'headers' : 'none';
}

/**
 * The dialog that sets new credentials for a MCP server entry. The new set
 * replaces the whole old set, so every value is given again. The values live in
 * the fields only: React never holds them, and the fields are cleared after
 * the request.
 */
export function McpCredentialsDialog({
  server,
  onSaved,
  onClose,
}: {
  server: AIMCPServer;
  onSaved(): void;
  onClose(): void;
}): ReactElement {
  const { ts } = useI18n();
  const client = useClient();
  const appBar = useContext(AppBarContext);
  const remoteNode = appBar.selectedRemoteNode || 'local';
  const credentials = useCredentialFields(
    currentMode(server),
    (server.headers ?? []).map((header) => header.name)
  );
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const hadCredentials = currentMode(server) !== 'none';

  const submit = async () => {
    const collected = credentials.collect();
    if (!collected.ok) return;
    setBusy(true);
    setFailure(null);
    try {
      const result = await client.PUT('/ai/mcp-servers/{name}/credentials', {
        params: {
          path: { name: server.name },
          query: { remoteNode, workspace: scopeQuery(server.scope) },
        },
        body: {
          ...(collected.token !== undefined ? { token: collected.token } : {}),
          ...(collected.headers !== undefined
            ? { headers: collected.headers }
            : {}),
        },
      });
      if (result.error) {
        setFailure(
          describeMcpApiError(
            ts,
            result.response.status,
            result.error,
            ts('The credentials were not saved.')
          )
        );
        return;
      }
      onSaved();
    } catch {
      setFailure(ts('The server did not respond.'));
    } finally {
      credentials.clear();
      setBusy(false);
    }
  };

  return (
    <Dialog open onOpenChange={(open) => !open && !busy && onClose()}>
      <DialogContent className="max-h-[90vh] max-w-lg overflow-y-auto">
        <DialogHeader>
          <DialogTitle>
            {ts('Rotate the credentials of {name}', { name: server.name })}
          </DialogTitle>
          <DialogDescription>
            {ts(
              'The new credentials replace all the old credentials. Give every value again. The server never shows a value after you save.'
            )}
          </DialogDescription>
        </DialogHeader>
        <form
          className="space-y-3"
          autoComplete="off"
          onSubmit={(event) => {
            event.preventDefault();
            void submit();
          }}
        >
          <CredentialFields
            state={credentials}
            keep={
              credentials.mode === 'none' && hadCredentials
                ? ts('The entry will have no credential.')
                : undefined
            }
          />
          {failure && (
            <p role="alert" className="text-sm text-destructive">
              {failure}
            </p>
          )}
          <DialogFooter>
            <Button
              type="button"
              variant="ghost"
              disabled={busy}
              onClick={onClose}
            >
              {ts('Cancel')}
            </Button>
            <Button type="submit" variant="primary" disabled={busy}>
              {ts('Rotate credentials')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

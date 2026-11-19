// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { TriangleAlert } from 'lucide-react';
import { useContext, useId, useState, type ReactElement } from 'react';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { AppBarContext } from '@/contexts/AppBarContext';
import { scopeQuery, scopeValue } from '@/features/ai-endpoints/endpoint';
import {
  buildMcpCreateBody,
  buildMcpUpdateBody,
  describeMcpApiError,
  emptyMcpForm,
  formFromServer,
  mcpProblemText,
  validateMcpForm,
  type AIMCPServer,
  type McpProblems,
  type McpServerForm,
} from '@/features/ai-endpoints/mcpServer';
import { useDagChoices } from '@/features/visual-editor/inspector/sections/tools/useToolSources';
import {
  ChatField,
  errorIssue,
} from '@/features/visual-editor/inspector/sections/chat/fields';
import { SELECT_CLASS } from '@/features/visual-editor/inspector/sections/RunEditor';
import { useClient } from '@/hooks/api';
import { useDebouncedValue } from '@/hooks/useDebouncedValue';
import { useI18n } from '@/i18n/I18nProvider';
import { AllowField } from './AllowField';
import { CredentialFields, useCredentialFields } from './CredentialFields';

/**
 * The dialog adds a MCP server entry with its credentials, or changes the URL and
 * the use rule of one that exists. The credentials of an entry that exists
 * change in their own dialog. The values live in password fields only.
 */
export function McpServerDialog({
  server,
  servers,
  onSaved,
  onClose,
}: {
  /** The entry to change, or undefined to add one. */
  server?: AIMCPServer;
  servers: readonly AIMCPServer[];
  onSaved(): void;
  onClose(): void;
}): ReactElement {
  const { ts } = useI18n();
  const client = useClient();
  const appBar = useContext(AppBarContext);
  const remoteNode = appBar.selectedRemoteNode || 'local';
  const creating = server === undefined;
  const id = useId();
  const credentials = useCredentialFields();

  const [form, setForm] = useState<McpServerForm>(() =>
    server ? formFromServer(server) : emptyMcpForm()
  );
  const [submitted, setSubmitted] = useState(false);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [dagQuery, setDagQuery] = useState('');
  const dags = useDagChoices(true, useDebouncedValue(dagQuery, 250));

  const taken = servers
    .filter((item) => item.scope === scopeValue(form.workspace))
    .map((item) => item.name);
  const problems: McpProblems = validateMcpForm(form, { creating, taken });
  // A name that breaks the rule shows at once. Other problems show after a save.
  const shown = (field: keyof McpProblems): string | undefined => {
    const problem = problems[field];
    if (!problem) return undefined;
    if (!submitted && !(field === 'name' && form.name !== '')) return undefined;
    return mcpProblemText(ts, problem);
  };
  const workspaceNames = (appBar.workspaces ?? []).map((item) => item.name);
  const set = (patch: Partial<McpServerForm>) =>
    setForm((current) => ({ ...current, ...patch }));
  const plainHttp = form.url.trim().toLowerCase().startsWith('http://');

  const submit = async () => {
    setSubmitted(true);
    const collected = creating ? credentials.collect() : { ok: true as const };
    if (Object.keys(problems).length > 0 || !collected.ok) return;
    setBusy(true);
    setFailure(null);
    try {
      const result = server
        ? await client.PATCH('/ai/mcp-servers/{name}', {
            params: {
              path: { name: server.name },
              query: { remoteNode, workspace: scopeQuery(server.scope) },
            },
            body: buildMcpUpdateBody(server, form),
          })
        : await client.POST('/ai/mcp-servers', {
            params: { query: { remoteNode } },
            body: {
              ...buildMcpCreateBody(form),
              ...('token' in collected && collected.token !== undefined
                ? { token: collected.token }
                : {}),
              ...('headers' in collected && collected.headers !== undefined
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
            ts('The MCP server was not saved.')
          )
        );
        return;
      }
      onSaved();
    } catch {
      setFailure(ts('The server did not respond.'));
    } finally {
      // The values are cleared after each request, whatever the result.
      credentials.clear();
      setBusy(false);
    }
  };

  return (
    <Dialog open onOpenChange={(open) => !open && !busy && onClose()}>
      <DialogContent className="max-h-[90vh] max-w-xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle>
            {creating
              ? ts('Add a MCP server')
              : ts('Edit {name}', { name: server.name })}
          </DialogTitle>
          <DialogDescription>
            {ts(
              'A MCP server entry holds a URL and credentials. A step names it. The step never sees the credentials.'
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
          <ChatField
            label={ts('Name')}
            htmlFor={`${id}-name`}
            help={
              creating
                ? ts(
                    'A step writes this name in its mcp entry, with no url. You cannot change it after you save.'
                  )
                : undefined
            }
            issues={
              shown('name') ? [errorIssue(shown('name') ?? '')] : undefined
            }
          >
            <Input
              id={`${id}-name`}
              value={form.name}
              readOnly={!creating}
              autoComplete="off"
              spellCheck={false}
              aria-invalid={shown('name') ? true : undefined}
              placeholder="docs"
              className="h-7 font-mono text-xs read-only:cursor-default read-only:bg-muted read-only:text-muted-foreground"
              onChange={(event) => set({ name: event.target.value })}
            />
          </ChatField>

          <ChatField
            label={ts('URL')}
            htmlFor={`${id}-url`}
            help={ts(
              'The streamable HTTP address of the MCP server. The coordinator calls it, not the worker.'
            )}
            issues={shown('url') ? [errorIssue(shown('url') ?? '')] : undefined}
          >
            <Input
              id={`${id}-url`}
              value={form.url}
              autoComplete="off"
              spellCheck={false}
              inputMode="url"
              aria-invalid={shown('url') ? true : undefined}
              placeholder="https://docs.example.com/mcp"
              className="h-7 font-mono text-xs"
              onChange={(event) => set({ url: event.target.value })}
            />
            {plainHttp && (
              <p className="mt-0.5 flex items-center gap-1 text-xs text-warning">
                <TriangleAlert className="size-3" aria-hidden="true" />
                {ts(
                  'This URL uses http. The credentials go over the network with no encryption.'
                )}
              </p>
            )}
          </ChatField>

          {creating ? (
            <CredentialFields state={credentials} />
          ) : (
            <p className="text-xs text-muted-foreground">
              {ts(
                'To change the credentials, use "Rotate credentials" in the list.'
              )}
            </p>
          )}

          <ChatField label={ts('Scope')} htmlFor={`${id}-scope`}>
            {creating ? (
              <select
                id={`${id}-scope`}
                className={SELECT_CLASS}
                value={form.workspace ?? ''}
                onChange={(event) =>
                  set({
                    workspace: event.target.value || null,
                    allowWorkspaces: [],
                  })
                }
              >
                <option value="">{ts('Global')}</option>
                {workspaceNames.map((name) => (
                  <option key={name} value={name}>
                    {ts('Workspace {name}', { name })}
                  </option>
                ))}
              </select>
            ) : (
              <p id={`${id}-scope`} className="text-xs">
                {form.workspace === null
                  ? ts('Global')
                  : ts('Workspace {name}', { name: form.workspace })}
              </p>
            )}
          </ChatField>

          {form.workspace === null && (
            <AllowField
              label={ts('Allowed workspaces')}
              allLabel={ts('All workspaces')}
              help={ts('A DAG with no workspace matches only All workspaces.')}
              value={form.allowWorkspaces}
              suggestions={workspaceNames}
              problem={shown('allowWorkspaces')}
              onChange={(next) => set({ allowWorkspaces: next })}
            />
          )}
          <AllowField
            label={ts('Allowed DAGs')}
            allLabel={ts('All DAGs')}
            help={ts('A step of a DAG in this list can use the MCP server.')}
            value={form.allowDags}
            suggestions={dags.dags}
            problem={shown('allowDags')}
            onChange={(next) => set({ allowDags: next })}
            onQuery={setDagQuery}
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
              {creating ? ts('Add MCP server') : ts('Save')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

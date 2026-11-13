// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { TriangleAlert } from 'lucide-react';
import { useContext, useId, useRef, useState, type ReactElement } from 'react';
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
import { AIEndpointProtocol } from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import {
  authOptions,
  buildCreateBody,
  buildUpdateBody,
  describeApiError,
  findPreset,
  findRow,
  formForPreset,
  formFromEndpoint,
  isCustomPreset,
  isLocalPreset,
  isPlainHttp,
  scopeQuery,
  scopeValue,
  keyProblem,
  validateForm,
  type AIEndpoint,
  type AIEndpointPreset,
  type EndpointForm,
  type Problem,
  type Problems,
} from '@/features/ai-endpoints/endpoint';
import { problemText } from '@/features/ai-endpoints/problemText';
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

/** The dialog adds an endpoint, or changes the fields of one that exists. */
export function EndpointDialog({
  endpoint,
  presets,
  endpoints,
  onSaved,
  onClose,
}: {
  /** The endpoint to change, or undefined to add one. */
  endpoint?: AIEndpoint;
  presets: readonly AIEndpointPreset[];
  endpoints: readonly AIEndpoint[];
  onSaved(): void;
  onClose(): void;
}): ReactElement {
  const { ts } = useI18n();
  const client = useClient();
  const appBar = useContext(AppBarContext);
  const remoteNode = appBar.selectedRemoteNode || 'local';
  const creating = endpoint === undefined;
  const id = useId();
  // The key lives in the field only. React never holds it.
  const keyRef = useRef<HTMLInputElement>(null);

  const [form, setForm] = useState<EndpointForm>(() => {
    if (endpoint) return formFromEndpoint(endpoint);
    const first = presets.find((item) => item.id === 'anthropic') ?? presets[0];
    return first
      ? formForPreset(
          first,
          first.protocols[0]?.protocol ?? AIEndpointProtocol.openai
        )
      : formForPreset(
          { id: 'custom', label: 'Custom', needsKey: true, protocols: [] },
          AIEndpointProtocol.openai
        );
  });
  const [submitted, setSubmitted] = useState(false);
  const [keyIssue, setKeyIssue] = useState<Problem | null>(null);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [dagQuery, setDagQuery] = useState('');
  const dags = useDagChoices(true, useDebouncedValue(dagQuery, 250));

  const preset = findPreset(presets, form.preset);
  const local = isLocalPreset(preset);
  const custom = isCustomPreset(form.preset);
  const taken = endpoints
    .filter((item) => item.scope === scopeValue(form.workspace))
    .map((item) => item.name);
  const problems: Problems = validateForm(form, {
    creating,
    preset,
    taken,
  });
  // A name that breaks the rule shows at once. Other problems show after a save.
  const shown = (field: keyof Problems): string | undefined => {
    const problem = problems[field];
    if (!problem) return undefined;
    if (!submitted && !(field === 'name' && form.name !== '')) return undefined;
    return problemText(ts, problem);
  };
  const keyText = keyIssue ? problemText(ts, keyIssue) : undefined;
  const workspaceNames = (appBar.workspaces ?? []).map((item) => item.name);

  const set = (patch: Partial<EndpointForm>) =>
    setForm((current) => ({ ...current, ...patch }));

  const changePreset = (presetId: string) => {
    const next = findPreset(presets, presetId);
    if (!next) return;
    const protocol = findRow(next, form.protocol)
      ? form.protocol
      : (next.protocols[0]?.protocol ?? form.protocol);
    setForm((current) =>
      formForPreset(next, protocol, {
        name: current.name,
        description: current.description,
        workspace: current.workspace,
        allowWorkspaces: current.allowWorkspaces,
        allowDags: current.allowDags,
      })
    );
  };
  const changeProtocol = (protocol: AIEndpointProtocol) => {
    if (!preset) return;
    setForm((current) =>
      formForPreset(preset, protocol, {
        name: current.name,
        description: current.description,
        workspace: current.workspace,
        allowWorkspaces: current.allowWorkspaces,
        allowDags: current.allowDags,
      })
    );
  };

  const submit = async () => {
    const key = keyRef.current?.value ?? '';
    const keyFound = keyProblem(key, creating && !local);
    setSubmitted(true);
    setKeyIssue(creating ? keyFound : null);
    if (Object.keys(problems).length > 0 || (creating && keyFound)) return;
    setBusy(true);
    setFailure(null);
    try {
      const result = endpoint
        ? await client.PATCH('/ai/endpoints/{name}', {
            params: {
              path: { name: endpoint.name },
              query: { remoteNode, workspace: scopeQuery(endpoint.scope) },
            },
            body: buildUpdateBody(endpoint, form),
          })
        : await client.POST('/ai/endpoints', {
            params: { query: { remoteNode } },
            body: {
              ...buildCreateBody(form),
              ...(key === '' ? {} : { key }),
            },
          });
      if (result.error) {
        setFailure(
          describeApiError(
            ts,
            result.response.status,
            result.error,
            ts('The endpoint was not saved.')
          )
        );
        return;
      }
      onSaved();
    } catch {
      setFailure(ts('The server did not respond.'));
    } finally {
      // The key is cleared after each request, whatever the result.
      if (keyRef.current) keyRef.current.value = '';
      setBusy(false);
    }
  };

  const protocols = preset?.protocols ?? [];
  return (
    <Dialog open onOpenChange={(open) => !open && !busy && onClose()}>
      <DialogContent className="max-h-[90vh] max-w-xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle>
            {creating
              ? ts('Add an endpoint')
              : ts('Edit {name}', { name: endpoint.name })}
          </DialogTitle>
          <DialogDescription>
            {ts(
              'An endpoint holds the address and the key of a model API. A step names it. The step never sees the key.'
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
            label={ts('Preset')}
            htmlFor={`${id}-preset`}
            help={
              protocols.some((item) => item.protocol === 'anthropic')
                ? undefined
                : ts('No agent step can use this endpoint yet.')
            }
          >
            <select
              id={`${id}-preset`}
              className={SELECT_CLASS}
              value={form.preset}
              disabled={!creating}
              onChange={(event) => changePreset(event.target.value)}
            >
              {presets.map((item) => (
                <option key={item.id} value={item.id}>
                  {item.label}
                </option>
              ))}
            </select>
          </ChatField>

          {protocols.length > 1 && (
            <ChatField
              label={ts('Protocol')}
              htmlFor={`${id}-protocol`}
              help={
                form.protocol === 'anthropic'
                  ? ts(
                      'The CLI claude of an agent.run step uses this protocol.'
                    )
                  : ts('No step uses this protocol yet.')
              }
            >
              <select
                id={`${id}-protocol`}
                className={SELECT_CLASS}
                value={form.protocol}
                disabled={!creating}
                onChange={(event) =>
                  changeProtocol(event.target.value as AIEndpointProtocol)
                }
              >
                {protocols.map((item) => (
                  <option key={item.protocol} value={item.protocol}>
                    {item.protocol === 'anthropic'
                      ? ts('Anthropic (CLI claude)')
                      : ts('OpenAI')}
                  </option>
                ))}
              </select>
            </ChatField>
          )}

          <ChatField
            label={ts('Name')}
            htmlFor={`${id}-name`}
            help={
              creating
                ? ts(
                    'A step writes this name in its endpoint key. You cannot change it after you save.'
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
              placeholder="anthropic-team"
              className="h-7 font-mono text-xs read-only:cursor-default read-only:bg-muted read-only:text-muted-foreground"
              onChange={(event) => set({ name: event.target.value })}
            />
          </ChatField>

          <ChatField
            label={ts('Description')}
            htmlFor={`${id}-description`}
            issues={
              shown('description')
                ? [errorIssue(shown('description') ?? '')]
                : undefined
            }
          >
            <Input
              id={`${id}-description`}
              value={form.description}
              autoComplete="off"
              className="h-7 text-xs"
              onChange={(event) => set({ description: event.target.value })}
            />
          </ChatField>

          <ChatField
            label={ts('Base URL')}
            htmlFor={`${id}-url`}
            help={
              local
                ? ts(
                    'A local model can use http. The name localhost is the host of the coordinator, not of the worker.'
                  )
                : undefined
            }
            issues={
              shown('baseUrl')
                ? [errorIssue(shown('baseUrl') ?? '')]
                : undefined
            }
          >
            <Input
              id={`${id}-url`}
              value={form.baseUrl}
              autoComplete="off"
              spellCheck={false}
              inputMode="url"
              aria-invalid={shown('baseUrl') ? true : undefined}
              placeholder={custom || local ? 'https://api.example.com/v1' : ''}
              className="h-7 font-mono text-xs"
              onChange={(event) => set({ baseUrl: event.target.value })}
            />
            {isPlainHttp(form.baseUrl) && (
              <p className="mt-0.5 flex items-center gap-1 text-xs text-warning">
                <TriangleAlert className="size-3" aria-hidden="true" />
                {ts(
                  'This URL uses http. The key goes over the network with no encryption.'
                )}
              </p>
            )}
          </ChatField>

          <ChatField label={ts('Authentication')} htmlFor={`${id}-auth`}>
            <select
              id={`${id}-auth`}
              className={SELECT_CLASS}
              value={form.auth}
              onChange={(event) =>
                set({ auth: event.target.value as EndpointForm['auth'] })
              }
            >
              {authOptions(form.protocol).map((option) => (
                <option key={option} value={option}>
                  {option}
                </option>
              ))}
            </select>
          </ChatField>

          {custom && (
            <ChatField
              label={ts('Models URL')}
              htmlFor={`${id}-models`}
              help={ts('Optional. The page that lists the models.')}
              issues={
                shown('modelsUrl')
                  ? [errorIssue(shown('modelsUrl') ?? '')]
                  : undefined
              }
            >
              <Input
                id={`${id}-models`}
                value={form.modelsUrl}
                autoComplete="off"
                spellCheck={false}
                className="h-7 font-mono text-xs"
                onChange={(event) => set({ modelsUrl: event.target.value })}
              />
            </ChatField>
          )}

          {creating ? (
            <ChatField
              label={local ? ts('Key (optional)') : ts('Key')}
              htmlFor={`${id}-key`}
              help={
                local
                  ? ts(
                      'A local model needs no key. The server never shows a key after you save.'
                    )
                  : ts(
                      'The server never shows the key again. It shows the first characters and a fingerprint.'
                    )
              }
              issues={keyText ? [errorIssue(keyText)] : undefined}
            >
              <input
                ref={keyRef}
                id={`${id}-key`}
                type="password"
                name={`${id}-secret`}
                autoComplete="new-password"
                spellCheck={false}
                aria-invalid={keyText ? true : undefined}
                className="h-7 w-full min-w-0 rounded-md border border-input bg-card px-2 font-mono text-xs shadow-sm focus-visible:border-ring focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/20 aria-invalid:border-destructive"
              />
            </ChatField>
          ) : (
            <p className="text-xs text-muted-foreground">
              {ts('To change the key, use "Rotate key" in the list.')}
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
            help={ts('A step of a DAG in this list can use the endpoint.')}
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
              {creating ? ts('Add endpoint') : ts('Save')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

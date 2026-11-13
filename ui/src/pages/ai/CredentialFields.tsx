// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Plus, X } from 'lucide-react';
import { useId, useRef, useState, type ReactElement } from 'react';
import { Button } from '@/components/ui/button';
import { IconButton } from '@/components/ui/icon-button';
import { Input } from '@/components/ui/input';
import {
  headerNameProblems,
  MAX_HEADERS,
  mcpProblemText,
  valueProblem,
  valueTooLong,
  type CredentialMode,
  type HeaderRow,
  type McpProblem,
} from '@/features/ai-endpoints/mcpServer';
import {
  ChatField,
  errorIssue,
} from '@/features/visual-editor/inspector/sections/chat/fields';
import { SELECT_CLASS } from '@/features/visual-editor/inspector/sections/RunEditor';
import { useI18n } from '@/i18n/I18nProvider';

const SECRET_INPUT_CLASS =
  'h-7 w-full min-w-0 rounded-md border border-input bg-card px-2 font-mono text-xs shadow-sm focus-visible:border-ring focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/20 aria-invalid:border-destructive';

/** What the credential fields give when the user submits: the values, or the problems. */
export type CredentialResult =
  | {
      ok: true;
      token: string | undefined;
      headers: Record<string, string> | undefined;
    }
  | { ok: false };

/**
 * The state of the credential fields. The values live in the password fields
 * only: React holds the mode and the names of the headers, never a token or a
 * value. `collect` reads the fields when the form is sent, and `clear` empties
 * them after each request, whatever its result.
 */
export function useCredentialFields(
  initialMode: CredentialMode = 'none',
  initialNames: readonly string[] = []
) {
  const nextId = useRef(initialNames.length + 1);
  const [mode, setMode] = useState<CredentialMode>(initialMode);
  const [rows, setRows] = useState<HeaderRow[]>(() =>
    initialNames.map((name, index) => ({ id: index + 1, name }))
  );
  const [problems, setProblems] = useState<{
    token?: McpProblem;
    headers?: McpProblem;
    rows: Map<number, McpProblem>;
    values: Map<number, McpProblem>;
  }>({ rows: new Map(), values: new Map() });
  const tokenRef = useRef<HTMLInputElement>(null);
  const valueRefs = useRef(new Map<number, HTMLInputElement>());

  const changeMode = (next: CredentialMode) => {
    setMode(next);
    setProblems({ rows: new Map(), values: new Map() });
    if (next === 'headers' && rows.length === 0) {
      setRows([{ id: nextId.current++, name: '' }]);
    }
  };
  const addRow = () =>
    setRows((current) =>
      current.length >= MAX_HEADERS
        ? current
        : [...current, { id: nextId.current++, name: '' }]
    );
  const removeRow = (id: number) => {
    valueRefs.current.delete(id);
    setRows((current) => current.filter((row) => row.id !== id));
  };
  const renameRow = (id: number, name: string) =>
    setRows((current) =>
      current.map((row) => (row.id === id ? { ...row, name } : row))
    );

  const clear = () => {
    if (tokenRef.current) tokenRef.current.value = '';
    for (const input of valueRefs.current.values()) input.value = '';
  };

  const collect = (): CredentialResult => {
    const found = {
      rows: headerNameProblems(rows, mode),
      values: new Map<number, McpProblem>(),
    } as {
      token?: McpProblem;
      headers?: McpProblem;
      rows: Map<number, McpProblem>;
      values: Map<number, McpProblem>;
    };
    let token: string | undefined;
    let headers: Record<string, string> | undefined;
    if (mode === 'token') {
      const value = tokenRef.current?.value ?? '';
      if (value === '') found.token = 'token-required';
      else found.token = valueProblem(value) ?? undefined;
      if (found.token === undefined && valueTooLong(value)) {
        found.token = 'value-long';
      }
      token = value;
    }
    if (mode === 'headers') {
      if (rows.length === 0) found.headers = 'headers-empty';
      else if (rows.length > MAX_HEADERS) found.headers = 'headers-many';
      headers = {};
      for (const row of rows) {
        const value = valueRefs.current.get(row.id)?.value ?? '';
        if (value === '') found.values.set(row.id, 'header-value-required');
        else {
          const problem =
            valueProblem(value) ?? (valueTooLong(value) ? 'value-long' : null);
          if (problem) found.values.set(row.id, problem);
        }
        headers[row.name.toLowerCase()] = value;
      }
    }
    setProblems(found);
    const bad =
      found.token !== undefined ||
      found.headers !== undefined ||
      found.rows.size > 0 ||
      found.values.size > 0;
    if (bad) return { ok: false };
    return { ok: true, token, headers };
  };

  return {
    mode,
    rows,
    problems,
    tokenRef,
    valueRefs,
    changeMode,
    addRow,
    removeRow,
    renameRow,
    collect,
    clear,
  };
}

export type CredentialState = ReturnType<typeof useCredentialFields>;

/**
 * The fields of the credentials of a MCP server entry: no credential, a bearer
 * token, or headers with a write-only value each. The values are uncontrolled
 * password fields. The page never shows a value after the save.
 */
export function CredentialFields({
  state,
  keep,
}: {
  state: CredentialState;
  /** The note under the choice: what the form does with the credentials that exist. */
  keep?: string;
}): ReactElement {
  const { ts } = useI18n();
  const id = useId();
  const { mode, rows, problems } = state;
  return (
    <div className="space-y-3" data-testid="mcp-credentials">
      <ChatField label={ts('Credentials')} htmlFor={`${id}-mode`} help={keep}>
        <select
          id={`${id}-mode`}
          className={SELECT_CLASS}
          value={mode}
          onChange={(event) =>
            state.changeMode(event.target.value as CredentialMode)
          }
        >
          <option value="none">{ts('None')}</option>
          <option value="token">{ts('Bearer token')}</option>
          <option value="headers">{ts('Headers')}</option>
        </select>
      </ChatField>

      {mode === 'token' && (
        <ChatField
          label={ts('Token')}
          htmlFor={`${id}-token`}
          help={ts(
            'The coordinator sends it as authorization: Bearer. The server never shows it again. It shows the first characters and a fingerprint.'
          )}
          issues={
            problems.token
              ? [errorIssue(mcpProblemText(ts, problems.token))]
              : undefined
          }
        >
          <input
            ref={state.tokenRef}
            id={`${id}-token`}
            type="password"
            name={`${id}-secret`}
            autoComplete="new-password"
            spellCheck={false}
            aria-invalid={problems.token ? true : undefined}
            className={SECRET_INPUT_CLASS}
          />
        </ChatField>
      )}

      {mode === 'headers' && (
        <fieldset className="space-y-2" aria-label={ts('Headers')}>
          <p className="text-[11px] text-muted-foreground">
            {ts(
              'The coordinator sends these headers to the server. The server never shows a value again.'
            )}
          </p>
          {rows.map((row, index) => {
            const nameProblem = problems.rows.get(row.id);
            const valueProblemOfRow = problems.values.get(row.id);
            return (
              <div
                key={row.id}
                className="grid grid-cols-[1fr_1fr_auto] items-start gap-1.5"
                data-testid="mcp-header-row"
              >
                <ChatField
                  label={
                    <span className="sr-only">
                      {ts('Header name {number}', { number: index + 1 })}
                    </span>
                  }
                  htmlFor={`${id}-name-${row.id}`}
                  issues={
                    nameProblem
                      ? [errorIssue(mcpProblemText(ts, nameProblem))]
                      : undefined
                  }
                >
                  <Input
                    id={`${id}-name-${row.id}`}
                    value={row.name}
                    placeholder="X-Api-Key"
                    autoComplete="off"
                    spellCheck={false}
                    aria-invalid={nameProblem ? true : undefined}
                    className="h-7 font-mono text-xs"
                    onChange={(event) =>
                      state.renameRow(row.id, event.target.value)
                    }
                  />
                </ChatField>
                <ChatField
                  label={
                    <span className="sr-only">
                      {ts('Header value {number}', { number: index + 1 })}
                    </span>
                  }
                  htmlFor={`${id}-value-${row.id}`}
                  issues={
                    valueProblemOfRow
                      ? [errorIssue(mcpProblemText(ts, valueProblemOfRow))]
                      : undefined
                  }
                >
                  <input
                    ref={(element) => {
                      if (element) state.valueRefs.current.set(row.id, element);
                      else state.valueRefs.current.delete(row.id);
                    }}
                    id={`${id}-value-${row.id}`}
                    type="password"
                    name={`${id}-secret-${row.id}`}
                    autoComplete="new-password"
                    spellCheck={false}
                    aria-invalid={valueProblemOfRow ? true : undefined}
                    className={SECRET_INPUT_CLASS}
                  />
                </ChatField>
                <IconButton
                  size="sm"
                  className="mt-0.5"
                  label={ts('Remove header {number}', { number: index + 1 })}
                  icon={X}
                  onClick={() => state.removeRow(row.id)}
                />
              </div>
            );
          })}
          {problems.headers && (
            <p role="alert" className="text-xs text-destructive">
              {mcpProblemText(ts, problems.headers)}
            </p>
          )}
          <Button
            type="button"
            variant="outline"
            className="h-7 gap-1 px-2 text-xs"
            disabled={rows.length >= MAX_HEADERS}
            onClick={state.addRow}
          >
            <Plus className="size-3.5" aria-hidden="true" />
            {ts('Add header')}
          </Button>
        </fieldset>
      )}
    </div>
  );
}

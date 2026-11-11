// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { components } from '@/api/v1/schema';
import {
  allowProblem,
  describeRefusal,
  sameList,
  scopeValue,
  scopeWorkspace,
  type ApiError,
  type RefusalRule,
  type RefusalTexts,
} from './endpoint';

export type AIMCPServer = components['schemas']['AIMCPServer'];
export type AIMCPServerAllow = components['schemas']['AIMCPServerAllow'];
export type AIMCPCredential = components['schemas']['AIMCPCredential'];
export type AIMCPHeaderCredential =
  components['schemas']['AIMCPHeaderCredential'];
export type CreateAIMCPServerRequest =
  components['schemas']['CreateAIMCPServerRequest'];
export type UpdateAIMCPServerRequest =
  components['schemas']['UpdateAIMCPServerRequest'];

/** The MCP tab of the AI page. */
export const MCP_PATH = '/ai/mcp';

/** The name rule of a MCP server entry (Spec 095, The MCP tab). */
export const MCP_NAME_PATTERN = /^[a-z][a-z0-9-]{0,63}$/;
/** The name of the tool bridge: an entry cannot use it. */
export const RESERVED_MCP_NAME = 'dagu';
/** The limits of the credentials of an entry. */
export const MAX_HEADERS = 16;
export const MAX_VALUE_BYTES = 8192;
/** A header name: the characters of a secret ref after the lower case. */
export const HEADER_NAME_PATTERN = /^[A-Za-z][A-Za-z0-9-]{0,63}$/;
/** The headers that the coordinator sets itself, and the hop-by-hop headers. */
export const RESERVED_HEADERS: readonly string[] = [
  'host',
  'content-length',
  'content-type',
  'accept',
  'mcp-session-id',
  'mcp-protocol-version',
  'connection',
  'keep-alive',
  'proxy-authenticate',
  'proxy-authorization',
  'te',
  'trailer',
  'transfer-encoding',
  'upgrade',
];

/** How an entry authenticates: a bearer token, headers, or nothing. */
export type CredentialMode = 'none' | 'token' | 'headers';

/** The state of the form that adds or changes an entry. It has no credential value. */
export interface McpServerForm {
  name: string;
  url: string;
  /** The name of a workspace, or null for the global scope. */
  workspace: string | null;
  allowWorkspaces: string[];
  allowDags: string[];
}

export function emptyMcpForm(): McpServerForm {
  return {
    name: '',
    url: '',
    workspace: null,
    allowWorkspaces: [],
    allowDags: [],
  };
}

/** The form of an entry that exists, for a change. */
export function formFromServer(server: AIMCPServer): McpServerForm {
  return {
    name: server.name,
    url: server.url,
    workspace: scopeWorkspace(server.scope),
    allowWorkspaces: [...(server.allow.workspaces ?? [])],
    allowDags: [...server.allow.dags],
  };
}

export type McpFieldName =
  | 'name'
  | 'url'
  | 'allowWorkspaces'
  | 'allowDags'
  | 'token'
  | 'headers';

/** A problem of a field. `mcpProblemText` turns it into a sentence, so that it can be translated. */
export type McpProblem =
  | 'name-pattern'
  | 'name-reserved'
  | 'name-exists'
  | 'url-required'
  | 'url-invalid'
  | 'url-scheme'
  | 'url-parts'
  | 'allow-empty'
  | 'token-required'
  | 'value-control'
  | 'value-long'
  | 'headers-empty'
  | 'headers-many'
  | 'header-name'
  | 'header-reserved'
  | 'header-twice'
  | 'header-value-required';

export type McpProblems = Partial<Record<McpFieldName, McpProblem>>;

export interface McpFormContext {
  creating: boolean;
  /** The names of the entries that are in the scope of the form. */
  taken: readonly string[];
}

/** Checks a URL as the server does: absolute, https or http, no user information and no fragment. */
export function mcpUrlProblem(raw: string): McpProblem | null {
  const value = raw.trim();
  if (value === '') return 'url-required';
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    return 'url-invalid';
  }
  if (url.protocol !== 'https:' && url.protocol !== 'http:') {
    return 'url-scheme';
  }
  if (url.username || url.password || url.hash || value.includes('#')) {
    return 'url-parts';
  }
  return null;
}

/** Checks the form. The credential values are checked apart, as the form never holds them. */
export function validateMcpForm(
  form: McpServerForm,
  ctx: McpFormContext
): McpProblems {
  const problems: McpProblems = {};
  if (ctx.creating) {
    if (form.name === RESERVED_MCP_NAME) problems.name = 'name-reserved';
    else if (!MCP_NAME_PATTERN.test(form.name)) problems.name = 'name-pattern';
    else if (ctx.taken.includes(form.name)) problems.name = 'name-exists';
  }
  const url = mcpUrlProblem(form.url);
  if (url) problems.url = url;
  const dags = allowProblem(form.allowDags);
  if (dags) problems.allowDags = dags;
  if (form.workspace === null) {
    const workspaces = allowProblem(form.allowWorkspaces);
    if (workspaces) problems.allowWorkspaces = workspaces;
  }
  return problems;
}

/** A header row of the credential form: the name is state, the value lives in its field. */
export interface HeaderRow {
  /** A key that React uses for the row. */
  id: number;
  name: string;
}

/** The size of a text in bytes, as the server counts it. */
function byteLength(value: string): number {
  return new TextEncoder().encode(value).length;
}

/** Checks a token or a header value typed in a password field. Only the server knows more. */
export function valueProblem(value: string): McpProblem | null {
  // eslint-disable-next-line no-control-regex
  if (/[\u0000-\u001f\u007f]/.test(value)) return 'value-control';
  return null;
}

/** True when the value is longer than the server accepts. */
export function valueTooLong(value: string): boolean {
  return byteLength(value) > MAX_VALUE_BYTES;
}

/** The names of the header rows, checked as the server checks them. */
export function headerNameProblems(
  rows: readonly HeaderRow[],
  mode: CredentialMode
): Map<number, McpProblem> {
  const problems = new Map<number, McpProblem>();
  if (mode !== 'headers') return problems;
  const seen = new Set<string>();
  for (const row of rows) {
    const lower = row.name.toLowerCase();
    if (!HEADER_NAME_PATTERN.test(row.name))
      problems.set(row.id, 'header-name');
    else if (RESERVED_HEADERS.includes(lower)) {
      problems.set(row.id, 'header-reserved');
    } else if (seen.has(lower)) problems.set(row.id, 'header-twice');
    seen.add(lower);
  }
  return problems;
}

/** The body of the create request, without the credentials. The caller adds them. */
export function buildMcpCreateBody(
  form: McpServerForm
): Omit<CreateAIMCPServerRequest, 'token' | 'headers'> {
  return {
    name: form.name,
    url: form.url.trim(),
    scope: scopeValue(form.workspace),
    allow: allowOf(form),
  };
}

function allowOf(form: McpServerForm): AIMCPServerAllow {
  return form.workspace === null
    ? { workspaces: form.allowWorkspaces, dags: form.allowDags }
    : { dags: form.allowDags };
}

/** The body of the change request: the fields that differ from the entry. */
export function buildMcpUpdateBody(
  server: AIMCPServer,
  form: McpServerForm
): UpdateAIMCPServerRequest {
  const body: UpdateAIMCPServerRequest = {};
  if (form.url.trim() !== server.url) body.url = form.url.trim();
  const before = server.allow;
  if (
    !sameList(form.allowDags, before.dags) ||
    (form.workspace === null &&
      !sameList(form.allowWorkspaces, before.workspaces ?? []))
  ) {
    body.allow = allowOf(form);
  }
  return body;
}

/** The host of a URL, for the list: it never shows a query or a path. */
export function urlHost(raw: string): string {
  try {
    return new URL(raw).host || raw;
  } catch {
    return raw;
  }
}

/** The credentials of an entry as the list shows them. */
export interface CredentialDisplay {
  /** `token` for the bearer token, or the name of the header. */
  label: string;
  prefix: string | undefined;
  fingerprint: string;
}

export function credentialsOf(server: AIMCPServer): CredentialDisplay[] {
  const out: CredentialDisplay[] = [];
  if (server.token) {
    out.push({
      label: 'token',
      prefix: server.token.keyPrefix,
      fingerprint: server.token.keyFingerprint,
    });
  }
  for (const header of server.headers ?? []) {
    out.push({
      label: header.name,
      prefix: header.keyPrefix,
      fingerprint: header.keyFingerprint,
    });
  }
  return out;
}

/** The time of the last write of any credential of an entry, or undefined. */
export function credentialsUpdatedAt(server: AIMCPServer): string | undefined {
  const times = [
    server.token?.keyUpdatedAt,
    ...(server.headers ?? []).map((header) => header.keyUpdatedAt),
  ].filter((time): time is string => time !== undefined);
  return times.length === 0 ? undefined : [...times].sort()[times.length - 1];
}

type Translate = (
  text: string,
  params?: Record<string, string | number>
) => string;

/** The sentence of a problem of the form. */
export function mcpProblemText(ts: Translate, problem: McpProblem): string {
  switch (problem) {
    case 'name-pattern':
      return ts(
        'The name must start with a lowercase letter. Use lowercase letters, digits and -. The limit is 64 characters.'
      );
    case 'name-reserved':
      return ts(
        'The name dagu is the name of the tool bridge. Use another name.'
      );
    case 'name-exists':
      return ts('A MCP server with this name exists in this scope.');
    case 'url-required':
      return ts('Give the URL of the MCP server.');
    case 'url-invalid':
      return ts('The URL is not valid.');
    case 'url-scheme':
      return ts('The URL must start with https or http.');
    case 'url-parts':
      return ts('The URL must not have user information or a fragment.');
    case 'allow-empty':
      return ts('Choose at least one name, or All.');
    case 'token-required':
      return ts('Give the token.');
    case 'value-control':
      return ts('The value has a control character. Paste it again.');
    case 'value-long':
      return ts('The value is too long. The limit is 8192 bytes.');
    case 'headers-empty':
      return ts('Add at least one header, or choose another form.');
    case 'headers-many':
      return ts('An entry has at most 16 headers.');
    case 'header-name':
      return ts(
        'A header name starts with a letter. Use letters, digits and -.'
      );
    case 'header-reserved':
      return ts('Dagu sets this header. Use another name.');
    case 'header-twice':
      return ts('This header appears twice.');
    case 'header-value-required':
      return ts('Give the value of the header.');
  }
}

/** Turns a refusal of a MCP server route into a sentence. See `describeRefusal`. */
export function describeMcpApiError(
  ts: Translate,
  status: number | undefined,
  error: ApiError | undefined,
  fallback: string
): string {
  return describeRefusal(ts, status, error, fallback, TEXTS, RULES);
}

const TEXTS: RefusalTexts = {
  forbidden: 'Only an administrator can manage the MCP servers.',
  exists: 'A MCP server with this name exists in this scope.',
  missing: 'The MCP server does not exist. Reload the page.',
  unavailable: 'The server does not store MCP servers.',
};

const RULES: RefusalRule[] = [
  {
    test: /^name .*expected a match/,
    text: 'The name must start with a lowercase letter. Use lowercase letters, digits and -. The limit is 64 characters.',
  },
  {
    test: /is the name of the tool bridge/,
    text: 'The name dagu is the name of the tool bridge. Use another name.',
  },
  {
    test: /expected the scheme https|the scheme http is valid only/,
    text: 'The URL must start with https or http.',
  },
  { test: /expected an absolute URL/, text: 'The URL is not valid.' },
  {
    test: /user information|must not have a fragment/,
    text: 'The URL must not have user information or a fragment.',
  },
  {
    test: /a token and a header authorization contradict/,
    text: 'A token is the header authorization. Remove one of them.',
  },
  {
    test: /header name .* is not valid: the coordinator sets it/,
    text: 'Dagu sets this header. Use another name.',
  },
  {
    test: /header name .*expected a match/,
    text: 'A header name starts with a letter. Use letters, digits and -.',
  },
  { test: /headers has \d+ items/, text: 'An entry has at most 16 headers.' },
  { test: /must not be empty/, text: 'The value is empty.' },
  {
    test: /has \d+ bytes, above the limit/,
    text: 'The value is too long. The limit is 8192 bytes.',
  },
  {
    test: /must not have a control character/,
    text: 'The value has a control character. Paste it again.',
  },
  {
    test: /allow\.workspaces is required/,
    text: 'Choose at least one workspace, or all workspaces.',
  },
  {
    test: /allow\.dags is required/,
    text: 'Choose at least one DAG, or all DAGs.',
  },
  {
    test: /must be the only item/,
    text: 'The value * must be the only item of its list.',
  },
];

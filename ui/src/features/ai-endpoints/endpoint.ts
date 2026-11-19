// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  AIEndpointAuth as Auth,
  AIEndpointProtocol as Protocol,
  type components,
} from '@/api/v1/schema';

export type AIEndpoint = components['schemas']['AIEndpoint'];
export type AIEndpointAllow = components['schemas']['AIEndpointAllow'];
export type AIEndpointAuth = components['schemas']['AIEndpointAuth'];
export type AIEndpointProtocol = components['schemas']['AIEndpointProtocol'];
export type AIEndpointPreset = components['schemas']['AIEndpointPreset'];
export type AIEndpointPresetProtocol =
  components['schemas']['AIEndpointPresetProtocol'];
export type CreateAIEndpointRequest =
  components['schemas']['CreateAIEndpointRequest'];
export type UpdateAIEndpointRequest =
  components['schemas']['UpdateAIEndpointRequest'];

/** The AI page: the list of endpoints is its Endpoints tab. */
export const ENDPOINTS_PATH = '/ai/endpoints';

/** The name rule of an endpoint (Spec 093, Fields). */
export const NAME_PATTERN = /^[a-z][a-z0-9-]{0,63}$/;
export const MAX_DESCRIPTION = 200;
/** The value that matches each workspace or each DAG. */
export const WILDCARD = '*';
export const GLOBAL_SCOPE = 'global';
const WORKSPACE_PREFIX = 'workspace:';

/** The authentication forms that an endpoint of a protocol accepts. */
export function authOptions(protocol: AIEndpointProtocol): AIEndpointAuth[] {
  return protocol === Protocol.anthropic
    ? [Auth.x_api_key, Auth.bearer, Auth.anthropic_oauth]
    : [Auth.bearer, Auth.x_api_key];
}

/** The workspace of a scope, or null for the global scope. */
export function scopeWorkspace(scope: string): string | null {
  return scope.startsWith(WORKSPACE_PREFIX)
    ? scope.slice(WORKSPACE_PREFIX.length)
    : null;
}

/** The scope value of the API for a workspace name, or for none. */
export function scopeValue(workspace: string | null): string {
  return workspace ? `${WORKSPACE_PREFIX}${workspace}` : GLOBAL_SCOPE;
}

/** The value of the query `workspace` for the scope of an endpoint or a MCP server. */
export function scopeQuery(scope: string): string {
  return scopeWorkspace(scope) ?? GLOBAL_SCOPE;
}

/** The preset of an endpoint, or undefined when the server has no such preset. */
export function findPreset(
  presets: readonly AIEndpointPreset[],
  id: string
): AIEndpointPreset | undefined {
  return presets.find((preset) => preset.id === id);
}

/** The row of a protocol of a preset. */
export function findRow(
  preset: AIEndpointPreset | undefined,
  protocol: AIEndpointProtocol
): AIEndpointPresetProtocol | undefined {
  return preset?.protocols.find((row) => row.protocol === protocol);
}

/** True for a preset that serves a local model: it needs no key and allows http. */
export function isLocalPreset(preset: AIEndpointPreset | undefined): boolean {
  return preset !== undefined && !preset.needsKey;
}

/** True for a preset whose base URL the administrator types. */
export function isCustomPreset(id: string): boolean {
  return id === 'custom';
}

/** The state of the form that adds or changes an endpoint. It has no key. */
export interface EndpointForm {
  preset: string;
  protocol: AIEndpointProtocol;
  name: string;
  description: string;
  baseUrl: string;
  auth: AIEndpointAuth;
  modelsUrl: string;
  /** The name of a workspace, or null for the global scope. */
  workspace: string | null;
  allowWorkspaces: string[];
  allowDags: string[];
}

/** The defaults that a row of a preset gives to the form. */
export function formForPreset(
  preset: AIEndpointPreset,
  protocol: AIEndpointProtocol,
  keep?: Partial<EndpointForm>
): EndpointForm {
  const row = findRow(preset, protocol);
  return {
    name: '',
    description: '',
    workspace: null,
    allowWorkspaces: [],
    allowDags: [],
    ...keep,
    preset: preset.id,
    protocol,
    baseUrl: row?.baseUrl ?? '',
    auth: row?.auth ?? authOptions(protocol)[0] ?? Auth.bearer,
    // The text {base_url} of the table stands for the base URL; the server
    // reads it from the preset, so a typed value matters for custom only.
    modelsUrl: '',
  };
}

/** The form of an endpoint that exists, for a change. */
export function formFromEndpoint(endpoint: AIEndpoint): EndpointForm {
  return {
    preset: endpoint.preset,
    protocol: endpoint.protocol,
    name: endpoint.name,
    description: endpoint.description ?? '',
    baseUrl: endpoint.baseUrl,
    auth: endpoint.auth,
    modelsUrl: endpoint.modelsUrl ?? '',
    workspace: scopeWorkspace(endpoint.scope),
    allowWorkspaces: [...(endpoint.allow.workspaces ?? [])],
    allowDags: [...endpoint.allow.dags],
  };
}

export type FieldName =
  | 'name'
  | 'description'
  | 'baseUrl'
  | 'modelsUrl'
  | 'allowWorkspaces'
  | 'allowDags'
  | 'key';

/** A problem of a field. The text is in `describeProblem`, so that it can be translated. */
export type Problem =
  | 'name-pattern'
  | 'name-exists'
  | 'description-long'
  | 'url-required'
  | 'url-invalid'
  | 'url-scheme'
  | 'url-parts'
  | 'allow-empty'
  | 'key-required'
  | 'key-control';

export type Problems = Partial<Record<FieldName, Problem>>;

/** What the form needs to judge a value: the other names of the scope and the preset. */
export interface FormContext {
  creating: boolean;
  preset: AIEndpointPreset | undefined;
  /** The names of the endpoints that are in the scope of the form. */
  taken: readonly string[];
}

/** Checks a URL as the server does: absolute, https (http for a local model and custom), no parts. */
export function urlProblem(raw: string, allowHttp: boolean): Problem | null {
  const value = raw.trim();
  if (value === '') return 'url-required';
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    return 'url-invalid';
  }
  if (url.protocol !== 'https:' && !(allowHttp && url.protocol === 'http:')) {
    return 'url-scheme';
  }
  if (url.username || url.password || url.search || url.hash) {
    return 'url-parts';
  }
  return null;
}

/** True for an http URL: the key goes over a network with no encryption. */
export function isPlainHttp(raw: string): boolean {
  return raw.trim().toLowerCase().startsWith('http://');
}

/** An allow list with no item: it gives no access, so the form refuses it. */
export function allowProblem(list: readonly string[]): 'allow-empty' | null {
  return list.length === 0 ? 'allow-empty' : null;
}

/** Checks the form. The key is checked apart, as the form never holds it. */
export function validateForm(form: EndpointForm, ctx: FormContext): Problems {
  const problems: Problems = {};
  if (ctx.creating) {
    if (!NAME_PATTERN.test(form.name)) problems.name = 'name-pattern';
    else if (ctx.taken.includes(form.name)) problems.name = 'name-exists';
  }
  if (form.description.length > MAX_DESCRIPTION) {
    problems.description = 'description-long';
  }
  const allowHttp = isLocalPreset(ctx.preset) || isCustomPreset(form.preset);
  const url = urlProblem(form.baseUrl, allowHttp);
  if (url) problems.baseUrl = url;
  if (isCustomPreset(form.preset) && form.modelsUrl.trim() !== '') {
    const models = urlProblem(form.modelsUrl, allowHttp);
    if (models) problems.modelsUrl = models;
  }
  const dags = allowProblem(form.allowDags);
  if (dags) problems.allowDags = dags;
  if (form.workspace === null) {
    const workspaces = allowProblem(form.allowWorkspaces);
    if (workspaces) problems.allowWorkspaces = workspaces;
  }
  return problems;
}

/** Checks a key typed in a password field. Only the server knows more. */
export function keyProblem(key: string, required: boolean): Problem | null {
  if (key === '') return required ? 'key-required' : null;
  // eslint-disable-next-line no-control-regex
  return /[\u0000-\u001f\u007f]/.test(key) ? 'key-control' : null;
}

/** The allow block of the form. A workspace endpoint has no `workspaces` list. */
function allowOf(form: EndpointForm): AIEndpointAllow {
  return form.workspace === null
    ? { workspaces: form.allowWorkspaces, dags: form.allowDags }
    : { dags: form.allowDags };
}

/** The body of the create request, without the key. The caller adds the key. */
export function buildCreateBody(
  form: EndpointForm
): Omit<CreateAIEndpointRequest, 'key'> {
  const body: Omit<CreateAIEndpointRequest, 'key'> = {
    name: form.name,
    preset: form.preset,
    protocol: form.protocol,
    baseUrl: form.baseUrl.trim(),
    auth: form.auth,
    scope: scopeValue(form.workspace),
    allow: allowOf(form),
  };
  if (form.description.trim() !== '')
    body.description = form.description.trim();
  if (isCustomPreset(form.preset) && form.modelsUrl.trim() !== '') {
    body.modelsUrl = form.modelsUrl.trim();
  }
  return body;
}

/** True when the two lists have the same items in the same order. */
export function sameList(a: readonly string[], b: readonly string[]): boolean {
  return a.length === b.length && a.every((item, index) => item === b[index]);
}

/** The body of the change request: the fields that differ from the endpoint. */
export function buildUpdateBody(
  endpoint: AIEndpoint,
  form: EndpointForm
): UpdateAIEndpointRequest {
  const body: UpdateAIEndpointRequest = {};
  if (form.description.trim() !== (endpoint.description ?? '')) {
    body.description = form.description.trim();
  }
  if (form.baseUrl.trim() !== endpoint.baseUrl) {
    body.baseUrl = form.baseUrl.trim();
  }
  if (form.auth !== endpoint.auth) body.auth = form.auth;
  if (
    isCustomPreset(endpoint.preset) &&
    form.modelsUrl.trim() !== (endpoint.modelsUrl ?? '')
  ) {
    body.modelsUrl = form.modelsUrl.trim();
  }
  const before = endpoint.allow;
  if (
    !sameList(form.allowDags, before.dags) ||
    (form.workspace === null &&
      !sameList(form.allowWorkspaces, before.workspaces ?? []))
  ) {
    body.allow = allowOf(form);
  }
  return body;
}

/** The most characters of a key prefix that a list shows (Spec 093, "The key"). */
const MAX_KEY_PREFIX = 6;
const FINGERPRINT_LENGTH = 12;

/**
 * The text of a write-only key or credential in a list: its first characters
 * and its fingerprint, never the value. The text never has more than 6
 * characters of the prefix and 12 of the fingerprint, whatever the server sends.
 */
export function keyText(
  prefix: string | undefined,
  fingerprint: string
): string {
  const head = (prefix ?? '').slice(0, MAX_KEY_PREFIX);
  const print = fingerprint.slice(0, FINGERPRINT_LENGTH);
  return head === '' ? print : `${head}… · ${print}`;
}

/** One line that says who may use an endpoint or a MCP server, for the list. */
export function allowSummary(
  allow: AIEndpointAllow,
  scope: string,
  text: {
    all: string;
    allDags: string;
    dags(count: number): string;
    workspaces(count: number): string;
    join(parts: string[]): string;
  }
): string {
  const dags =
    allow.dags.length === 1 && allow.dags[0] === WILDCARD
      ? text.allDags
      : text.dags(allow.dags.length);
  if (scopeWorkspace(scope) !== null) return dags;
  const list = allow.workspaces ?? [];
  const workspaces =
    list.length === 1 && list[0] === WILDCARD
      ? text.all
      : text.workspaces(list.length);
  return text.join([workspaces, dags]);
}

/** The names that the user can add to an allow list: the suggestions that are not in it yet. */
export function remainingNames(
  names: readonly string[],
  chosen: readonly string[],
  query: string
): string[] {
  const needle = query.trim().toLowerCase();
  return names.filter(
    (name) =>
      !chosen.includes(name) &&
      (needle === '' || name.toLowerCase().includes(needle))
  );
}

/** An error of the API, as openapi-fetch gives it. */
export interface ApiError {
  code?: string;
  message?: string;
}

const SERVER_PREFIX = /^invalid AI endpoint:\s*/;

type Translate = (
  text: string,
  params?: Record<string, string | number>
) => string;

/** A rule that turns a message of the server into a sentence of the UI. */
export interface RefusalRule {
  test: RegExp;
  text: string;
}

/** The sentences of the statuses that do not depend on the message. */
export interface RefusalTexts {
  forbidden: string;
  exists: string;
  missing: string;
  unavailable: string;
}

/**
 * Turns a refusal of the server into a sentence. The status and the message
 * select the sentence. A message that no rule knows is shown as the server
 * wrote it, without its prefix. The messages of the server never hold a key
 * or a credential value.
 */
export function describeRefusal(
  ts: Translate,
  status: number | undefined,
  error: ApiError | undefined,
  fallback: string,
  texts: RefusalTexts,
  rules: readonly RefusalRule[]
): string {
  const message = (error?.message ?? '').replace(SERVER_PREFIX, '').trim();
  if (status === 401 || status === 403) return ts(texts.forbidden);
  if (status === 409 || error?.code === 'already_exists') {
    return ts(texts.exists);
  }
  if (status === 404) {
    return message.toLowerCase().includes('workspace')
      ? ts('The workspace of the scope does not exist.')
      : ts(texts.missing);
  }
  if (status === 503) return ts(texts.unavailable);
  for (const rule of rules) {
    if (rule.test.test(message)) return ts(rule.text);
  }
  return message === '' ? fallback : message;
}

/** Turns a refusal of an endpoint route into a sentence. See `describeRefusal`. */
export function describeApiError(
  ts: Translate,
  status: number | undefined,
  error: ApiError | undefined,
  fallback: string
): string {
  return describeRefusal(ts, status, error, fallback, TEXTS, RULES);
}

const TEXTS: RefusalTexts = {
  forbidden: 'Only an administrator can manage the AI endpoints.',
  exists: 'An endpoint with this name exists in this scope.',
  missing: 'The endpoint does not exist. Reload the page.',
  unavailable: 'The server does not store AI endpoints.',
};

const RULES: RefusalRule[] = [
  {
    test: /^name .*expected a match/,
    text: 'The name must start with a lowercase letter. Use lowercase letters, digits and -. The limit is 64 characters.',
  },
  { test: /key is required/, text: 'This preset needs a key.' },
  { test: /key must not be empty/, text: 'The key is empty.' },
  {
    test: /key has \d+ bytes/,
    text: 'The key is too long. The limit is 8192 bytes.',
  },
  {
    test: /key must not have a control character/,
    text: 'The key has a control character. Paste it again.',
  },
  { test: /key is not valid UTF-8/, text: 'The key is not valid text.' },
  {
    test: /the scheme http is valid only for a local model/,
    text: 'Only a local model and Custom accept http. Use https.',
  },
  { test: /expected the scheme https/, text: 'The URL must start with https.' },
  { test: /expected an absolute URL/, text: 'The URL is not valid.' },
  {
    test: /user information|must not have a query|must not have a fragment/,
    text: 'The URL must not have user information, a query or a fragment.',
  },
  {
    test: /base_url is required/,
    text: 'This preset needs a base URL.',
  },
  {
    test: /valid with the protocol anthropic only/,
    text: 'The form anthropic-oauth is valid with the Anthropic protocol only.',
  },
  { test: /auth is required/, text: 'Choose an authentication form.' },
  {
    test: /models_url is valid with the preset custom only/,
    text: 'Only Custom takes a models URL.',
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
  {
    test: /description has more than/,
    text: 'The description has more than 200 characters.',
  },
  {
    test: /preset .* has no row for the protocol/,
    text: 'This preset has no such protocol.',
  },
];

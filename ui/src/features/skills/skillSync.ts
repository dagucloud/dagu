// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { components } from '@/api/v1/schema';
import type { useClient } from '@/hooks/api';
import { downloadBlob } from '@/lib/download';
import type { SkillIssue } from './skillIssues';

type Client = ReturnType<typeof useClient>;
type SkillWriteResult = components['schemas']['SkillWriteResult'];

// --- the hash chain ---

/**
 * Every write of a skill carries the hash that the editor read. A write gives
 * the new hash, and the next write carries it. A conflict does not touch the
 * draft: the state only remembers the hash that the server has now.
 */
export interface SyncState {
  /**
   * The hash that a write or a rebase gave. Null until one happens: the hash
   * of the loaded skill applies then.
   */
  base: string | null;
  /** Set after a 409: the hash on the server now. Empty if the skill is gone. */
  conflict: { hash: string } | null;
}

export type SyncAction =
  | { type: 'wrote'; hash: string }
  | { type: 'conflict'; hash: string }
  /** The user keeps the draft and takes the hash of the server. */
  | { type: 'rebase' }
  /** The user loads the server version again. */
  | { type: 'reset' };

export const INITIAL_SYNC: SyncState = { base: null, conflict: null };

export function syncReducer(state: SyncState, action: SyncAction): SyncState {
  switch (action.type) {
    case 'wrote':
      return { base: action.hash, conflict: null };
    case 'conflict':
      return { ...state, conflict: { hash: action.hash } };
    case 'rebase':
      if (!state.conflict || state.conflict.hash === '') return state;
      return { base: state.conflict.hash, conflict: null };
    case 'reset':
      return INITIAL_SYNC;
  }
}

/** The hash that the next write sends: the chained hash, else the loaded one. */
export function writeHash(
  state: SyncState,
  loaded: string | undefined
): string {
  return state.base ?? loaded ?? '';
}

/** The first characters of a hash, for a message. */
export function shortHash(hash: string): string {
  return hash.slice(0, 8);
}

// --- the answers of the write routes ---

export type WriteFailure =
  | { kind: 'conflict'; hash: string; message: string }
  | { kind: 'invalid'; issues: SkillIssue[]; message: string }
  | { kind: 'too-large' | 'forbidden' | 'missing' | 'other'; message: string };

export type WriteOutcome =
  | { ok: true; result: SkillWriteResult }
  | ({ ok: false } & WriteFailure);

interface ErrorBody {
  message?: string;
  issues?: SkillIssue[];
  hash?: string;
}

/** Sorts the answer of a write route by its status code. */
export function classifyFailure(
  status: number,
  body: ErrorBody | undefined,
  fallback: string
): WriteFailure {
  const message = body?.message || fallback;
  switch (status) {
    case 409:
      return { kind: 'conflict', hash: body?.hash ?? '', message };
    case 400:
      return { kind: 'invalid', issues: body?.issues ?? [], message };
    case 413:
      return { kind: 'too-large', message };
    case 403:
      return { kind: 'forbidden', message };
    case 404:
      return { kind: 'missing', message };
    default:
      return { kind: 'other', message };
  }
}

interface Target {
  name: string;
  remoteNode: string;
}

async function guarded(
  run: () => Promise<{
    data?: SkillWriteResult;
    error?: unknown;
    response: Response;
  }>,
  fallback: string
): Promise<WriteOutcome> {
  try {
    const { data, error, response } = await run();
    if (data && response.ok) return { ok: true, result: data };
    return {
      ok: false,
      ...classifyFailure(response.status, error as ErrorBody, fallback),
    };
  } catch (failure) {
    return {
      ok: false,
      kind: 'other',
      message: failure instanceof Error ? failure.message : fallback,
    };
  }
}

/** Writes SKILL.md. `hash` is the hash that the editor read, or `create`. */
export function writeSkillText(
  client: Client,
  target: Target,
  content: string,
  precondition: { hash: string } | 'create',
  fallback: string
): Promise<WriteOutcome> {
  return guarded(
    () =>
      client.PUT('/skills/{name}', {
        params: {
          path: { name: target.name },
          query: { remoteNode: target.remoteNode },
          header:
            precondition === 'create'
              ? { 'If-None-Match': '*' }
              : { 'If-Match': precondition.hash },
        },
        body: { content },
      }),
    fallback
  );
}

/** Writes one file under references/, assets/ or scripts/. */
export function writeSkillFile(
  client: Client,
  target: Target,
  path: string,
  body: string | Blob,
  hash: string,
  fallback: string
): Promise<WriteOutcome> {
  return guarded(
    () =>
      client.PUT('/skills/{name}/files', {
        params: {
          path: { name: target.name },
          query: { remoteNode: target.remoteNode, path },
          header: { 'If-Match': hash },
        },
        body: body as unknown as string,
        bodySerializer: (value: unknown) => value as BodyInit,
        headers: { 'Content-Type': 'application/octet-stream' },
      }),
    fallback
  );
}

/** Deletes one file of a skill. */
export function removeSkillFile(
  client: Client,
  target: Target,
  path: string,
  hash: string,
  fallback: string
): Promise<WriteOutcome> {
  return guarded(
    () =>
      client.DELETE('/skills/{name}/files', {
        params: {
          path: { name: target.name },
          query: { remoteNode: target.remoteNode, path },
          header: { 'If-Match': hash },
        },
      }),
    fallback
  );
}

/** Deletes the whole folder of a skill. */
export async function removeSkill(
  client: Client,
  target: Target,
  hash: string,
  fallback: string
): Promise<{ ok: true } | ({ ok: false } & WriteFailure)> {
  try {
    const { error, response } = await client.DELETE('/skills/{name}', {
      params: {
        path: { name: target.name },
        query: { remoteNode: target.remoteNode },
        header: { 'If-Match': hash },
      },
    });
    if (response.ok) return { ok: true };
    return {
      ok: false,
      ...classifyFailure(response.status, error as ErrorBody, fallback),
    };
  } catch (failure) {
    return {
      ok: false,
      kind: 'other',
      message: failure instanceof Error ? failure.message : fallback,
    };
  }
}

/** Reads one text file of a skill. Throws with the message of the server. */
export async function readSkillFileText(
  client: Client,
  target: Target,
  path: string,
  signal?: AbortSignal
): Promise<string> {
  const { data, error, response } = await client.GET('/skills/{name}/files', {
    params: {
      path: { name: target.name },
      query: { remoteNode: target.remoteNode, path },
    },
    parseAs: 'text',
    signal,
  });
  if (!response.ok || data === undefined) {
    throw new Error(
      (error as ErrorBody | undefined)?.message ||
        response.statusText ||
        'The file could not be read.'
    );
  }
  return data as unknown as string;
}

/** Reads the bytes of one file of a skill. Throws with the message of the server. */
export async function readSkillFileBlob(
  client: Client,
  target: Target,
  path: string,
  signal?: AbortSignal
): Promise<Blob> {
  const { data, error, response } = await client.GET('/skills/{name}/files', {
    params: {
      path: { name: target.name },
      query: { remoteNode: target.remoteNode, path },
    },
    parseAs: 'blob',
    signal,
  });
  if (!response.ok || data === undefined) {
    throw new Error(
      (error as ErrorBody | undefined)?.message ||
        response.statusText ||
        'The file could not be read.'
    );
  }
  return data as unknown as Blob;
}

// --- the download ---

/**
 * The file name of a Content-Disposition header: `filename*` (RFC 5987) wins,
 * then `filename`. A path part is dropped. Returns the fallback when the
 * header has no usable name.
 */
export function attachmentName(
  header: string | null | undefined,
  fallback: string
): string {
  if (header) {
    const star = /filename\*\s*=\s*([^']*)'[^']*'([^;]+)/i.exec(header);
    let name: string | undefined;
    if (star) {
      try {
        name = decodeURIComponent(star[2]!.trim());
      } catch {
        name = undefined;
      }
    }
    if (!name) {
      const quoted = /filename\s*=\s*"((?:[^"\\]|\\.)*)"/i.exec(header);
      const bare = /filename\s*=\s*([^;"\s]+)/i.exec(header);
      name = quoted ? quoted[1]!.replace(/\\(.)/g, '$1') : bare?.[1];
    }
    name = name?.split(/[\\/]/).pop()?.trim();
    if (name) return name;
  }
  return fallback;
}

/** Downloads the ZIP of one or several skills and saves it with the name of the server. */
export async function downloadSkillZip(
  client: Client,
  remoteNode: string,
  names: readonly string[]
): Promise<string> {
  const { data, error, response } = await client.POST('/skills/download', {
    params: { query: { remoteNode } },
    body: { names: [...names] },
    parseAs: 'blob',
  });
  if (!response.ok || data === undefined) {
    throw new Error(
      (error as ErrorBody | undefined)?.message ||
        response.statusText ||
        'The download failed.'
    );
  }
  const fallback = names.length === 1 ? `${names[0]}.zip` : 'skills.zip';
  const filename = attachmentName(
    response.headers.get('Content-Disposition'),
    fallback
  );
  downloadBlob(data as unknown as Blob, filename);
  return filename;
}

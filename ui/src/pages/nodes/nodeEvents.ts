// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { getAuthHeaders } from '@/lib/authHeaders';
import { handleAuthResponse } from '@/lib/authSession';

/** The endpoints that answer with a stream of node events. */
export type NodeEventPath = '/node-connection-tests' | '/node-host-keys';

/**
 * Posts body to a node test or trust endpoint and reads its server-sent
 * event stream: onResult gets each `result` event as it arrives, and the
 * promise resolves with the closing `done` event. A refused request, or a
 * stream that ends without `done`, rejects.
 */
export async function postNodeEvents<Result, Done>(
  apiURL: string,
  path: NodeEventPath,
  body: unknown,
  onResult: (result: Result) => void,
  signal?: AbortSignal
): Promise<Done> {
  const response = await fetch(`${apiURL}${path}`, {
    method: 'POST',
    headers: getAuthHeaders({ Accept: 'text/event-stream' }),
    body: JSON.stringify(body),
    signal,
  });
  handleAuthResponse(response);
  if (!response.ok || !response.body) {
    throw new Error(await refusalMessage(response));
  }

  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffered = '';
  for (;;) {
    const { done, value } = await reader.read();
    buffered += decoder.decode(value, { stream: !done });
    let end: number;
    while ((end = buffered.indexOf('\n\n')) >= 0) {
      const event = parseEvent(buffered.slice(0, end));
      buffered = buffered.slice(end + 2);
      if (event?.name === 'result') {
        onResult(JSON.parse(event.data) as Result);
      } else if (event?.name === 'done') {
        void reader.cancel();
        return JSON.parse(event.data) as Done;
      }
    }
    if (done) {
      throw new Error('The connection closed before every node finished.');
    }
  }
}

/** Parses one event of a stream. A keepalive comment has no event. */
function parseEvent(frame: string): { name: string; data: string } | null {
  let name = '';
  const data: string[] = [];
  for (const line of frame.split('\n')) {
    if (line.startsWith('event:')) {
      name = line.slice('event:'.length).trim();
    } else if (line.startsWith('data:')) {
      data.push(line.slice('data:'.length).trimStart());
    }
  }
  return name ? { name, data: data.join('\n') } : null;
}

async function refusalMessage(response: Response): Promise<string> {
  try {
    const body = (await response.json()) as { message?: string };
    if (body.message) {
      return body.message;
    }
  } catch {
    // A body that is not JSON carries no message.
  }
  return `Request failed with status ${response.status}`;
}

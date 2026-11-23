// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { afterEach, describe, expect, it, vi } from 'vitest';
import { postNodeEvents } from '../nodeEvents';

/** A response whose body is chunks, sent as they are split. */
function streamed(...chunks: string[]): Response {
  const encoder = new TextEncoder();
  return new Response(
    new ReadableStream<Uint8Array>({
      start(controller) {
        for (const chunk of chunks) {
          controller.enqueue(encoder.encode(chunk));
        }
        controller.close();
      },
    }),
    { status: 200, headers: { 'Content-Type': 'text/event-stream' } }
  );
}

describe('postNodeEvents', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('reads results split across chunks, skips keepalives, and ends with done', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(
        streamed(
          ': keepalive\n\nevent: result\ndata: {"node":"we',
          'b01","ok":true}\n\nevent: result\ndata: {"node":"db01","ok":false}\n',
          '\nevent: done\ndata: {"passed":1,"failed":1}\n\n'
        )
      );
    vi.stubGlobal('fetch', fetchMock);
    const results: unknown[] = [];

    const done = await postNodeEvents(
      '/api/v1',
      '/node-connection-tests',
      { nodes: ['web01', 'db01'] },
      (result) => results.push(result)
    );

    expect(results).toEqual([
      { node: 'web01', ok: true },
      { node: 'db01', ok: false },
    ]);
    expect(done).toEqual({ passed: 1, failed: 1 });
    const [url, init] = fetchMock.mock.calls[0]!;
    expect(url).toBe('/api/v1/node-connection-tests');
    expect(init.method).toBe('POST');
    expect(JSON.parse(init.body)).toEqual({ nodes: ['web01', 'db01'] });
  });

  it('rejects a stream that closes before done', async () => {
    vi.stubGlobal(
      'fetch',
      vi
        .fn()
        .mockResolvedValue(
          streamed('event: result\ndata: {"node":"web01","ok":true}\n\n')
        )
    );

    await expect(
      postNodeEvents('/api/v1', '/node-connection-tests', {}, () => {})
    ).rejects.toThrow('The connection closed before every node finished.');
  });

  it('rejects a refused request with its message', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            code: 'bad_request',
            message: 'no node is named web09',
          }),
          { status: 400, headers: { 'Content-Type': 'application/json' } }
        )
      )
    );

    await expect(
      postNodeEvents('/api/v1', '/node-connection-tests', {}, () => {})
    ).rejects.toThrow('no node is named web09');
  });
});

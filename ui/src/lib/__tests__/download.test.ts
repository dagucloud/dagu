// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { TOKEN_KEY } from '../authSession';
import { downloadBlob, downloadFromUrl } from '../download';

const createObjectURL = vi.fn(() => 'blob:mock');
const revokeObjectURL = vi.fn();

beforeEach(() => {
  // jsdom does not implement object URLs.
  vi.stubGlobal('URL', Object.assign(URL, { createObjectURL, revokeObjectURL }));
});

afterEach(() => {
  createObjectURL.mockClear();
  revokeObjectURL.mockClear();
  vi.unstubAllGlobals();
  localStorage.clear();
});

describe('downloadBlob', () => {
  it('saves through a temporary object URL with the given filename', () => {
    const click = vi.fn();
    const anchor = document.createElement('a');
    anchor.click = click;
    const createElement = vi
      .spyOn(document, 'createElement')
      .mockReturnValue(anchor);

    downloadBlob(new Blob(['content']), 'run.log');

    expect(createObjectURL).toHaveBeenCalledTimes(1);
    expect(anchor.download).toBe('run.log');
    expect(click).toHaveBeenCalled();
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:mock');
    createElement.mockRestore();
  });
});

describe('downloadFromUrl', () => {
  it('sends the auth token and uses the Content-Disposition filename', async () => {
    localStorage.setItem(TOKEN_KEY, 'token-1');
    const fetchMock = vi.fn(async () =>
      new Response(new Blob(['data']), {
        headers: { 'Content-Disposition': 'attachment; filename="server.log"' },
      })
    );
    vi.stubGlobal('fetch', fetchMock);
    const click = vi.fn();
    const anchor = document.createElement('a');
    anchor.click = click;
    const createElement = vi
      .spyOn(document, 'createElement')
      .mockReturnValue(anchor);

    await downloadFromUrl('/api/v1/log/download', 'fallback.log');

    expect(fetchMock).toHaveBeenCalledWith('/api/v1/log/download', {
      headers: { Authorization: 'Bearer token-1' },
    });
    expect(anchor.download).toBe('server.log');
    createElement.mockRestore();
  });

  it('falls back to the given filename without a Content-Disposition header', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(new Blob(['data']))));
    const anchor = document.createElement('a');
    anchor.click = vi.fn();
    const createElement = vi
      .spyOn(document, 'createElement')
      .mockReturnValue(anchor);

    await downloadFromUrl('/api/v1/log/download', 'fallback.log');

    expect(anchor.download).toBe('fallback.log');
    createElement.mockRestore();
  });

  it('rejects on a non-OK response', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response('nope', { status: 500 }))
    );

    await expect(
      downloadFromUrl('/api/v1/log/download', 'fallback.log')
    ).rejects.toThrow('Download failed');
  });
});

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
  it('saves through a temporary object URL and revokes it after the download starts', () => {
    vi.useFakeTimers();
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
    // Revoking in the click task could abort the navigation.
    expect(revokeObjectURL).not.toHaveBeenCalled();

    vi.advanceTimersByTime(1000);
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:mock');
    createElement.mockRestore();
    vi.useRealTimers();
  });
});

// A plain object instead of a real Response: constructing Response from a
// Blob needs Blob.stream, which the CI jsdom/Node combination lacks.
function fetchResponse({
  ok = true,
  statusText = 'OK',
  disposition,
}: {
  ok?: boolean;
  statusText?: string;
  disposition?: string;
} = {}) {
  return {
    ok,
    statusText,
    headers: {
      get: (name: string) =>
        name === 'Content-Disposition' ? (disposition ?? null) : null,
    },
    blob: async () => new Blob(['data']),
  };
}

describe('downloadFromUrl', () => {
  it('sends the auth token and uses the Content-Disposition filename', async () => {
    localStorage.setItem(TOKEN_KEY, 'token-1');
    const fetchMock = vi.fn(async () =>
      fetchResponse({ disposition: 'attachment; filename="server.log"' })
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
    vi.stubGlobal('fetch', vi.fn(async () => fetchResponse()));
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
      vi.fn(async () =>
        fetchResponse({ ok: false, statusText: 'Internal Server Error' })
      )
    );

    await expect(
      downloadFromUrl('/api/v1/log/download', 'fallback.log')
    ).rejects.toThrow('Download failed');
  });
});

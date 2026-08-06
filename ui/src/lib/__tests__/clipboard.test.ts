// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { afterEach, describe, expect, it, vi } from 'vitest';
import { copyText } from '../clipboard';

function stubClipboard(writeText: () => Promise<void>) {
  Object.defineProperty(navigator, 'clipboard', {
    value: { writeText },
    configurable: true,
  });
}

afterEach(() => {
  vi.restoreAllMocks();
  Reflect.deleteProperty(navigator, 'clipboard');
});

describe('copyText', () => {
  it('reports success when the Clipboard API accepts the text', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    stubClipboard(writeText);

    await expect(copyText('release-notes')).resolves.toBe(true);
    expect(writeText).toHaveBeenCalledWith('release-notes');
  });

  it('falls back to a selection copy when the Clipboard API rejects', async () => {
    stubClipboard(() => Promise.reject(new Error('denied')));
    const execCommand = vi.fn().mockReturnValue(true);
    document.execCommand = execCommand;

    await expect(copyText('release-notes')).resolves.toBe(true);
    expect(execCommand).toHaveBeenCalledWith('copy');
    expect(document.querySelector('textarea')).toBeNull();
  });

  it('reports failure when the fallback copy is refused', async () => {
    stubClipboard(() => Promise.reject(new Error('denied')));
    document.execCommand = vi.fn().mockReturnValue(false);

    await expect(copyText('release-notes')).resolves.toBe(false);
    expect(document.querySelector('textarea')).toBeNull();
  });

  it('reports failure and detaches the textarea when the fallback throws', async () => {
    stubClipboard(() => Promise.reject(new Error('denied')));
    document.execCommand = vi.fn().mockImplementation(() => {
      throw new Error('unsupported');
    });

    await expect(copyText('release-notes')).resolves.toBe(false);
    expect(document.querySelector('textarea')).toBeNull();
  });
});

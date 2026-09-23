// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, render, screen } from '@testing-library/react';
import React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useClient } from '@/hooks/api';
import { ArtifactThumbnail } from '../ArtifactThumbnail';

vi.mock('@/hooks/api', () => ({
  useClient: vi.fn(),
}));

const useClientMock = vi.mocked(useClient);
const run = { dagRunName: 'shop', dagRunId: 'run-1', remoteNode: 'local' };

type Download = { data: Blob; response: Response };

/** Returns a download whose response the test settles. */
function pendingDownload() {
  let resolve!: (download: Download) => void;
  const promise = new Promise<Download>((settle) => {
    resolve = settle;
  });
  return {
    promise,
    resolve: () =>
      resolve({ data: new Blob(['png']), response: new Response() }),
  };
}

describe('ArtifactThumbnail', () => {
  const createObjectURL = vi.fn();
  const revokeObjectURL = vi.fn();

  beforeEach(() => {
    createObjectURL.mockReset();
    revokeObjectURL.mockReset();
    let count = 0;
    createObjectURL.mockImplementation(() => `blob:${++count}`);
    Object.assign(URL, { createObjectURL, revokeObjectURL });
  });

  it('drops the previous image when the path changes', async () => {
    const first = pendingDownload();
    const second = pendingDownload();
    const GET = vi
      .fn()
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(second.promise);
    useClientMock.mockReturnValue({ GET } as never);

    const { rerender } = render(
      <ArtifactThumbnail run={run} path="browser/shop/01-cart.png" />
    );
    await act(async () => first.resolve());
    expect(screen.getByRole('img', { name: '01-cart.png' })).toHaveAttribute(
      'src',
      'blob:1'
    );

    rerender(<ArtifactThumbnail run={run} path="browser/shop/02-final.png" />);

    expect(screen.queryByRole('img')).not.toBeInTheDocument();
    expect(screen.getByText('02-final.png')).toBeInTheDocument();
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:1');
  });

  // The response arrives after the effect is cleaned up but before its
  // callback runs, so abort() cannot stop it.
  it('creates no URL for a download that settles after unmount', async () => {
    const download = pendingDownload();
    useClientMock.mockReturnValue({
      GET: vi.fn().mockReturnValue(download.promise),
    } as never);

    const { unmount } = render(
      <ArtifactThumbnail run={run} path="browser/shop/01-cart.png" />
    );
    download.resolve();
    unmount();
    await act(async () => {
      await download.promise;
    });

    expect(createObjectURL).not.toHaveBeenCalled();
  });
});

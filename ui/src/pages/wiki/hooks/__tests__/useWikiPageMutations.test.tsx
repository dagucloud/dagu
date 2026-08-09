// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useWikiPageMutations } from '../useWikiPageMutations';

const mocks = vi.hoisted(() => ({
  client: {
    DELETE: vi.fn(),
    POST: vi.fn(),
  },
  closeTab: vi.fn(),
  updateTab: vi.fn(),
  tabs: [] as Array<{
    id: string;
    wikiPagePath: string;
    title: string;
    workspace?: string | null;
  }>,
  unsavedTabIds: new Set<string>(),
}));

vi.mock('@/hooks/api', () => ({
  useClient: () => mocks.client,
}));

vi.mock('@/contexts/WikiPageTabContext', () => ({
  useWikiPageTabContext: () => ({
    tabs: mocks.tabs,
    unsavedTabIds: mocks.unsavedTabIds,
    closeTab: mocks.closeTab,
    updateTab: mocks.updateTab,
  }),
}));

describe('useWikiPageMutations', () => {
  const revalidateTree = vi.fn();

  beforeEach(() => {
    vi.resetAllMocks();
    mocks.tabs = [];
    mocks.unsavedTabIds = new Set();
  });

  it('updates only tabs beneath a renamed path in the same workspace', async () => {
    mocks.tabs = [
      {
        id: 'parent',
        wikiPagePath: 'guides',
        title: 'guides',
        workspace: 'ops',
      },
      {
        id: 'child',
        wikiPagePath: 'guides/runbook',
        title: 'runbook',
        workspace: 'ops',
      },
      {
        id: 'other-workspace',
        wikiPagePath: 'guides/runbook',
        title: 'runbook',
        workspace: 'platform',
      },
    ];
    mocks.client.POST.mockResolvedValue({ error: undefined });

    const { result } = renderHook(() =>
      useWikiPageMutations({ remoteNode: 'local', revalidateTree })
    );

    await act(async () => {
      expect(
        await result.current.changePath({
          oldPath: 'guides',
          newPath: 'manuals',
          workspace: 'ops',
          failureMessage: 'rename failed',
        })
      ).toBeNull();
    });

    expect(mocks.updateTab).toHaveBeenCalledTimes(2);
    expect(mocks.updateTab).toHaveBeenCalledWith('parent', {
      wikiPagePath: 'manuals',
      title: 'manuals',
    });
    expect(mocks.updateTab).toHaveBeenCalledWith('child', {
      wikiPagePath: 'manuals/runbook',
      title: 'runbook',
    });
    expect(revalidateTree).toHaveBeenCalledOnce();
  });

  it('closes only tabs deleted from the selected workspace', async () => {
    mocks.tabs = [
      {
        id: 'deleted',
        wikiPagePath: 'guides/runbook',
        title: 'runbook',
        workspace: 'ops',
      },
      {
        id: 'kept',
        wikiPagePath: 'guides/runbook',
        title: 'runbook',
        workspace: 'platform',
      },
    ];
    mocks.client.DELETE.mockResolvedValue({ error: undefined });

    const { result } = renderHook(() =>
      useWikiPageMutations({ remoteNode: 'local', revalidateTree })
    );

    await act(async () => {
      expect(await result.current.deletePath('guides', 'ops')).toBeNull();
    });

    expect(mocks.closeTab).toHaveBeenCalledOnce();
    expect(mocks.closeTab).toHaveBeenCalledWith('deleted');
  });

  it('groups batch deletes by workspace and reconciles successful paths', async () => {
    mocks.tabs = [
      {
        id: 'default-tab',
        wikiPagePath: 'readme',
        title: 'readme',
        workspace: null,
      },
      {
        id: 'ops-tab',
        wikiPagePath: 'runbooks/deploy',
        title: 'deploy',
        workspace: 'ops',
      },
    ];
    mocks.client.POST.mockResolvedValueOnce({
      data: { deleted: ['readme'], failed: [] },
      error: undefined,
    }).mockResolvedValueOnce({
      data: { deleted: ['runbooks'], failed: [] },
      error: undefined,
    });

    const { result } = renderHook(() =>
      useWikiPageMutations({ remoteNode: 'local', revalidateTree })
    );

    await act(async () => {
      await expect(
        result.current.deleteBatch([
          { path: 'readme', workspace: null },
          { path: 'runbooks', workspace: 'ops' },
        ])
      ).resolves.toEqual({ deletedCount: 2, failedCount: 0 });
    });

    expect(mocks.client.POST).toHaveBeenNthCalledWith(
      1,
      '/wiki/delete-batch',
      expect.objectContaining({
        params: { query: { remoteNode: 'local', workspace: 'default' } },
      })
    );
    expect(mocks.client.POST).toHaveBeenNthCalledWith(
      2,
      '/wiki/delete-batch',
      expect.objectContaining({
        params: { query: { remoteNode: 'local', workspace: 'ops' } },
      })
    );
    expect(mocks.closeTab).toHaveBeenCalledWith('default-tab');
    expect(mocks.closeTab).toHaveBeenCalledWith('ops-tab');
  });

  it('continues batch deletion when one workspace request rejects', async () => {
    mocks.client.POST.mockRejectedValueOnce(
      new Error('network error')
    ).mockResolvedValueOnce({
      data: { deleted: ['runbooks'], failed: [] },
      error: undefined,
    });

    const { result } = renderHook(() =>
      useWikiPageMutations({ remoteNode: 'local', revalidateTree })
    );

    await act(async () => {
      await expect(
        result.current.deleteBatch([
          { path: 'readme', workspace: null },
          { path: 'runbooks', workspace: 'ops' },
        ])
      ).resolves.toEqual({ deletedCount: 1, failedCount: 1 });
    });

    expect(mocks.client.POST).toHaveBeenCalledTimes(2);
    expect(revalidateTree).toHaveBeenCalledOnce();
  });
});

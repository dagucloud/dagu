// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';

import {
  wikiPageMutationHasUnsavedTabs,
  wikiPageMutationPathForTreeNode,
  wikiPageMutationTargetForTreeNode,
  isWorkspaceRootTreeNode,
  resolveWikiTreeMove,
} from '../wiki-page-mutation';

describe('page mutation path helpers', () => {
  it('normalizes all-view workspace tree paths before mutation', () => {
    expect(wikiPageMutationTargetForTreeNode('ops/docs/deploy', 'ops')).toEqual(
      {
        path: 'docs/deploy',
        workspace: 'ops',
      }
    );
    expect(wikiPageMutationPathForTreeNode('docs/deploy', null)).toBe(
      'docs/deploy'
    );
  });

  it('treats workspace root nodes as workspace targets, not Wiki page paths', () => {
    expect(isWorkspaceRootTreeNode('ops', 'ops')).toBe(true);
    expect(wikiPageMutationTargetForTreeNode('ops', 'ops')).toEqual({
      path: '',
      workspace: 'ops',
    });
  });

  it('resolves drag-and-drop moves within the same workspace', () => {
    expect(
      resolveWikiTreeMove({
        dragId: 'ops/docs/deploy',
        dragWorkspace: 'ops',
        parentId: 'ops/archive',
        parentWorkspace: 'ops',
      })
    ).toEqual({
      oldPath: 'docs/deploy',
      newPath: 'archive/deploy',
      workspace: 'ops',
    });

    expect(
      resolveWikiTreeMove({
        dragId: 'ops/docs/deploy',
        dragWorkspace: 'ops',
        parentId: 'ops',
        parentWorkspace: 'ops',
      })
    ).toEqual({
      oldPath: 'docs/deploy',
      newPath: 'deploy',
      workspace: 'ops',
    });
  });

  it('rejects drag-and-drop moves across workspaces', () => {
    expect(
      resolveWikiTreeMove({
        dragId: 'ops/docs/deploy',
        dragWorkspace: 'ops',
        parentId: 'prod/archive',
        parentWorkspace: 'prod',
      })
    ).toBeNull();
  });

  it('rejects directory moves into their own subtree', () => {
    expect(
      resolveWikiTreeMove({
        dragId: 'guides',
        parentId: 'guides/intro',
      })
    ).toBeNull();
    expect(
      resolveWikiTreeMove({
        dragId: 'guides',
        parentId: 'guides',
      })
    ).toBeNull();
  });

  it('resolves root drops inside the selected workspace', () => {
    expect(
      resolveWikiTreeMove({
        dragId: 'docs/deploy',
        dragWorkspace: 'ops',
        parentId: null,
        rootWorkspace: 'ops',
      })
    ).toEqual({
      oldPath: 'docs/deploy',
      newPath: 'deploy',
      workspace: 'ops',
    });
  });

  it('detects unsaved tabs affected by file and directory mutations', () => {
    const tabs = [
      { id: 'default', wikiPagePath: 'runbook', workspace: null },
      { id: 'ops-child', wikiPagePath: 'guides/deploy', workspace: 'ops' },
      { id: 'ops-other', wikiPagePath: 'notes', workspace: 'ops' },
    ];
    const unsaved = new Set(['default', 'ops-child']);

    expect(wikiPageMutationHasUnsavedTabs(tabs, unsaved, 'runbook')).toBe(true);
    expect(wikiPageMutationHasUnsavedTabs(tabs, unsaved, 'guides', 'ops')).toBe(
      true
    );
    expect(wikiPageMutationHasUnsavedTabs(tabs, unsaved, 'notes', 'ops')).toBe(
      false
    );
    expect(
      wikiPageMutationHasUnsavedTabs(tabs, unsaved, 'guides', 'other')
    ).toBe(false);
  });
});

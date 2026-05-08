// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';

import {
  docMutationPathForTreeNode,
  docMutationTargetForTreeNode,
  isWorkspaceRootTreeNode,
  resolveDocTreeMove,
} from '../doc-mutation';

describe('doc mutation path helpers', () => {
  it('normalizes all-view workspace tree paths before mutation', () => {
    expect(docMutationTargetForTreeNode('ops/runbooks/deploy', 'ops')).toEqual(
      {
        path: 'runbooks/deploy',
        workspace: 'ops',
      }
    );
    expect(docMutationPathForTreeNode('runbooks/deploy', null)).toBe(
      'runbooks/deploy'
    );
  });

  it('treats workspace root nodes as workspace targets, not document paths', () => {
    expect(isWorkspaceRootTreeNode('ops', 'ops')).toBe(true);
    expect(docMutationTargetForTreeNode('ops', 'ops')).toEqual({
      path: '',
      workspace: 'ops',
    });
  });

  it('resolves drag-and-drop moves within the same workspace', () => {
    expect(
      resolveDocTreeMove({
        dragId: 'ops/runbooks/deploy',
        dragWorkspace: 'ops',
        parentId: 'ops/archive',
        parentWorkspace: 'ops',
      })
    ).toEqual({
      oldPath: 'runbooks/deploy',
      newPath: 'archive/deploy',
      workspace: 'ops',
    });

    expect(
      resolveDocTreeMove({
        dragId: 'ops/runbooks/deploy',
        dragWorkspace: 'ops',
        parentId: 'ops',
        parentWorkspace: 'ops',
      })
    ).toEqual({
      oldPath: 'runbooks/deploy',
      newPath: 'deploy',
      workspace: 'ops',
    });
  });

  it('rejects drag-and-drop moves across workspaces', () => {
    expect(
      resolveDocTreeMove({
        dragId: 'ops/runbooks/deploy',
        dragWorkspace: 'ops',
        parentId: 'prod/archive',
        parentWorkspace: 'prod',
      })
    ).toBeNull();
  });
});

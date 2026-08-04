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
    expect(docMutationTargetForTreeNode('ops/docs/deploy', 'ops')).toEqual({
      path: 'docs/deploy',
      workspace: 'ops',
    });
    expect(docMutationPathForTreeNode('docs/deploy', null)).toBe('docs/deploy');
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
      resolveDocTreeMove({
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
      resolveDocTreeMove({
        dragId: 'ops/docs/deploy',
        dragWorkspace: 'ops',
        parentId: 'prod/archive',
        parentWorkspace: 'prod',
      })
    ).toBeNull();
  });

  it('resolves root drops inside the selected workspace', () => {
    expect(
      resolveDocTreeMove({
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
});

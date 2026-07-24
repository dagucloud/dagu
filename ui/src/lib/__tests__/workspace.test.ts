// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { beforeEach, describe, expect, it } from 'vitest';
import {
  ALL_WORKSPACES_DISPLAY_NAME,
  DEFAULT_WORKSPACE_DISPLAY_NAME,
  getStoredWorkspaceSelection,
  hasWorkspaceLabel,
  LEGACY_COCKPIT_WORKSPACE_STORAGE_KEY,
  LEGACY_WORKSPACE_SCOPE_STORAGE_KEY,
  WORKSPACE_STORAGE_KEY,
  WorkspaceKind,
  workspaceSelectionLabel,
  workspaceTargetSelectionQuery,
} from '../workspace';

describe('workspace labels', () => {
  it('uses concise labels for aggregate and default selections', () => {
    expect(workspaceSelectionLabel({ kind: WorkspaceKind.all })).toBe(
      ALL_WORKSPACES_DISPLAY_NAME
    );
    expect(workspaceSelectionLabel({ kind: WorkspaceKind.default })).toBe(
      DEFAULT_WORKSPACE_DISPLAY_NAME
    );
  });

  it('detects malformed workspace labels as workspace-labelled', () => {
    expect(hasWorkspaceLabel(['workspace=bad value'])).toBe(true);
    expect(hasWorkspaceLabel(['team=ops'])).toBe(false);
  });
});

describe('workspace storage', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it('falls through to cockpit workspace migration when legacy storage is invalid', () => {
    localStorage.setItem(WORKSPACE_STORAGE_KEY, 'invalid workspace');
    localStorage.setItem(LEGACY_COCKPIT_WORKSPACE_STORAGE_KEY, 'team-a');

    expect(getStoredWorkspaceSelection()).toEqual({
      kind: WorkspaceKind.workspace,
      workspace: 'team-a',
    });
    expect(
      localStorage.getItem(LEGACY_COCKPIT_WORKSPACE_STORAGE_KEY)
    ).toBeNull();
    expect(
      JSON.parse(localStorage.getItem(WORKSPACE_STORAGE_KEY) ?? '')
    ).toEqual({
      kind: WorkspaceKind.workspace,
      workspace: 'team-a',
    });
  });

  it('migrates the deprecated workspace storage shape', () => {
    localStorage.setItem(
      LEGACY_WORKSPACE_SCOPE_STORAGE_KEY,
      JSON.stringify({ scope: WorkspaceKind.default })
    );

    expect(getStoredWorkspaceSelection()).toEqual({
      kind: WorkspaceKind.default,
    });
    expect(localStorage.getItem(LEGACY_WORKSPACE_SCOPE_STORAGE_KEY)).toBeNull();
    expect(
      JSON.parse(localStorage.getItem(WORKSPACE_STORAGE_KEY) ?? '')
    ).toEqual({
      kind: WorkspaceKind.default,
    });
  });

  it('migrates deprecated stored legacy scope names', () => {
    localStorage.setItem(
      LEGACY_WORKSPACE_SCOPE_STORAGE_KEY,
      JSON.stringify({ scope: 'none' })
    );

    expect(getStoredWorkspaceSelection()).toEqual({
      kind: WorkspaceKind.default,
    });
    expect(localStorage.getItem(LEGACY_WORKSPACE_SCOPE_STORAGE_KEY)).toBeNull();
    expect(
      JSON.parse(localStorage.getItem(WORKSPACE_STORAGE_KEY) ?? '')
    ).toEqual({
      kind: WorkspaceKind.default,
    });

    localStorage.removeItem(WORKSPACE_STORAGE_KEY);
    localStorage.setItem(
      LEGACY_WORKSPACE_SCOPE_STORAGE_KEY,
      JSON.stringify({ scope: 'accessible' })
    );

    expect(getStoredWorkspaceSelection()).toEqual({
      kind: WorkspaceKind.all,
    });
    expect(localStorage.getItem(LEGACY_WORKSPACE_SCOPE_STORAGE_KEY)).toBeNull();
    expect(
      JSON.parse(localStorage.getItem(WORKSPACE_STORAGE_KEY) ?? '')
    ).toEqual({
      kind: WorkspaceKind.all,
    });
  });
});

describe('workspace target queries', () => {
  it('uses omitted workspace for default targets and workspace for named targets', () => {
    expect(workspaceTargetSelectionQuery({ kind: WorkspaceKind.all })).toBe(
      null
    );
    expect(
      workspaceTargetSelectionQuery({ kind: WorkspaceKind.default })
    ).toEqual({ workspace: WorkspaceKind.default });
    expect(
      workspaceTargetSelectionQuery({
        kind: WorkspaceKind.workspace,
        workspace: 'team-a',
      })
    ).toEqual({ workspace: 'team-a' });
  });
});

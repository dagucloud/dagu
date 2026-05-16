// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

export const WORKSPACE_LABEL_KEY = 'workspace';
export const WORKSPACE_LABEL_PREFIX = `${WORKSPACE_LABEL_KEY}=`;
export const WORKSPACE_STORAGE_KEY = 'dagu-selected-workspace';
export const LEGACY_WORKSPACE_SCOPE_STORAGE_KEY =
  'dagu-selected-workspace-scope';
export const LEGACY_COCKPIT_WORKSPACE_STORAGE_KEY = 'dagu_cockpit_workspace';
export const ALL_WORKSPACES_DISPLAY_NAME = 'all';
export const DEFAULT_WORKSPACE_DISPLAY_NAME = 'default';
export const WorkspaceKind = {
  all: 'all',
  default: 'default',
  workspace: 'workspace',
} as const;
export type WorkspaceKind = (typeof WorkspaceKind)[keyof typeof WorkspaceKind];
type StoredWorkspaceKind = WorkspaceKind | 'accessible' | 'none';

const WORKSPACE_NAME_PATTERN = /^[A-Za-z0-9_-]+$/;

export type WorkspaceSelection = {
  kind: WorkspaceKind;
  workspace?: string;
};

export type WorkspaceTargetQuery = {
  workspace?: string;
};

export function sanitizeWorkspaceName(name: string): string {
  const trimmed = name.trim();
  return isValidWorkspaceName(trimmed) ? trimmed : '';
}

export function isValidWorkspaceName(name: string): boolean {
  return (
    WORKSPACE_NAME_PATTERN.test(name) &&
    name.toLowerCase() !== WorkspaceKind.all &&
    name.toLowerCase() !== WorkspaceKind.default &&
    name.toLowerCase() !== 'global'
  );
}

export function workspaceLabel(name: string): string | undefined {
  const sanitized = sanitizeWorkspaceName(name);
  if (!sanitized || !isValidWorkspaceName(sanitized)) {
    return undefined;
  }
  return `${WORKSPACE_LABEL_PREFIX}${sanitized}`;
}

export function isWorkspaceLabel(label: string): boolean {
  return label.toLowerCase().startsWith(WORKSPACE_LABEL_PREFIX);
}

export function hasWorkspaceLabel(labels: string[] = []): boolean {
  return labels.some(isWorkspaceLabel);
}

export function withoutWorkspaceLabels(labels: string[] = []): string[] {
  return labels.filter((label) => !isWorkspaceLabel(label));
}

export function withWorkspaceLabel(
  labels: string[] = [],
  workspaceName: string
): string[] {
  const label = workspaceLabel(workspaceName);
  const filtered = withoutWorkspaceLabels(labels);
  return label ? [...filtered, label] : filtered;
}

export function workspaceNameFromLabels(labels: string[] = []): string {
  let workspaceName = '';
  for (const label of labels) {
    if (!isWorkspaceLabel(label)) {
      continue;
    }
    const value = sanitizeWorkspaceName(
      label.slice(WORKSPACE_LABEL_PREFIX.length)
    );
    if (!value) {
      return '';
    }
    if (workspaceName && workspaceName !== value) {
      return '';
    }
    workspaceName = value;
  }
  return workspaceName;
}

export function defaultWorkspaceSelection(): WorkspaceSelection {
  return { kind: WorkspaceKind.all };
}

export function sanitizeWorkspaceSelection(
  selection?: Partial<WorkspaceSelection> | null
): WorkspaceSelection {
  if (!selection) {
    return defaultWorkspaceSelection();
  }
  if (selection.kind === WorkspaceKind.default) {
    return { kind: WorkspaceKind.default };
  }
  if (selection.kind === WorkspaceKind.workspace) {
    const workspace = sanitizeWorkspaceName(selection.workspace ?? '');
    return workspace
      ? { kind: WorkspaceKind.workspace, workspace }
      : defaultWorkspaceSelection();
  }
  return defaultWorkspaceSelection();
}

export function workspaceNameForSelection(
  selection?: Partial<WorkspaceSelection> | null
): string {
  const sanitized = sanitizeWorkspaceSelection(selection);
  return sanitized.kind === WorkspaceKind.workspace
    ? (sanitized.workspace ?? '')
    : '';
}

export function isAggregateWorkspaceSelection(
  selection?: Partial<WorkspaceSelection> | null
): boolean {
  return sanitizeWorkspaceSelection(selection).kind === WorkspaceKind.all;
}

export function isMutableWorkspaceSelection(
  selection?: Partial<WorkspaceSelection> | null
): boolean {
  return sanitizeWorkspaceSelection(selection).kind !== WorkspaceKind.all;
}

export function workspaceSelectionLabel(
  selection?: Partial<WorkspaceSelection> | null
): string {
  const sanitized = sanitizeWorkspaceSelection(selection);
  switch (sanitized.kind) {
    case WorkspaceKind.default:
      return DEFAULT_WORKSPACE_DISPLAY_NAME;
    case WorkspaceKind.workspace:
      return sanitized.workspace ?? 'Workspace';
    case WorkspaceKind.all:
    default:
      return ALL_WORKSPACES_DISPLAY_NAME;
  }
}

export function workspaceSelectionKey(
  selection?: Partial<WorkspaceSelection> | null
): string {
  const sanitized = sanitizeWorkspaceSelection(selection);
  if (sanitized.kind === WorkspaceKind.workspace) {
    return `workspace:${sanitized.workspace}`;
  }
  return `workspace:${sanitized.kind}`;
}

export function workspaceSelectionQuery(
  selection?: Partial<WorkspaceSelection> | null
): { workspace: string } {
  const sanitized = sanitizeWorkspaceSelection(selection);
  if (sanitized.kind === WorkspaceKind.workspace) {
    return { workspace: sanitized.workspace ?? WorkspaceKind.all };
  }
  return { workspace: sanitized.kind };
}

export function workspaceTargetSelectionQuery(
  selection?: Partial<WorkspaceSelection> | null
): WorkspaceTargetQuery | null {
  const sanitized = sanitizeWorkspaceSelection(selection);
  if (sanitized.kind === WorkspaceKind.all) {
    return null;
  }
  if (sanitized.kind === WorkspaceKind.workspace) {
    return { workspace: sanitized.workspace };
  }
  return { workspace: WorkspaceKind.default };
}

export function workspaceDocumentSelectionQuery(
  selection?: Partial<WorkspaceSelection> | null
): WorkspaceTargetQuery | null {
  return workspaceTargetSelectionQuery(selection);
}

export function workspaceTargetQueryForWorkspace(
  workspace?: string | null
): WorkspaceTargetQuery {
  const sanitized = sanitizeWorkspaceName(workspace ?? '');
  if (!sanitized) {
    return { workspace: WorkspaceKind.default };
  }
  return { workspace: sanitized };
}

export const workspaceDocumentQueryForWorkspace =
  workspaceTargetQueryForWorkspace;

export function visibleDocumentPathForWorkspace(
  docPath: string,
  workspace?: string | null
): string {
  const sanitized = sanitizeWorkspaceName(workspace ?? '');
  if (!sanitized) {
    return docPath;
  }
  const prefix = `${sanitized}/`;
  return docPath.startsWith(prefix) ? docPath.slice(prefix.length) : docPath;
}

function normalizeStoredWorkspaceKind(kind: unknown): WorkspaceKind | null {
  switch (kind) {
    case 'accessible':
      return WorkspaceKind.all;
    case 'none':
      return WorkspaceKind.default;
    case WorkspaceKind.all:
    case WorkspaceKind.default:
    case WorkspaceKind.workspace:
      return kind;
    default:
      return null;
  }
}

function parseStoredWorkspaceSelection(
  value: string
): WorkspaceSelection | null {
  try {
    const parsed = JSON.parse(value) as {
      kind?: StoredWorkspaceKind;
      scope?: StoredWorkspaceKind;
      workspace?: unknown;
    };
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      return null;
    }
    const kind = normalizeStoredWorkspaceKind(parsed.kind ?? parsed.scope);
    if (kind === WorkspaceKind.default) {
      return { kind: WorkspaceKind.default };
    }
    if (kind === WorkspaceKind.workspace) {
      const workspace = sanitizeWorkspaceName(
        typeof parsed.workspace === 'string' ? parsed.workspace : ''
      );
      return workspace ? { kind: WorkspaceKind.workspace, workspace } : null;
    }
    if (kind === WorkspaceKind.all) {
      return { kind: WorkspaceKind.all };
    }
    return null;
  } catch {
    return null;
  }
}

export function getStoredWorkspaceSelection(): WorkspaceSelection {
  try {
    const stored = localStorage.getItem(WORKSPACE_STORAGE_KEY);
    if (stored !== null) {
      const parsed = parseStoredWorkspaceSelection(stored);
      if (parsed) {
        return parsed;
      }

      const sanitized = sanitizeWorkspaceName(stored);
      localStorage.removeItem(WORKSPACE_STORAGE_KEY);
      if (sanitized) {
        const selection = {
          kind: WorkspaceKind.workspace,
          workspace: sanitized,
        };
        persistWorkspaceSelection(selection);
        return selection;
      }
    }

    const legacyScope = localStorage.getItem(
      LEGACY_WORKSPACE_SCOPE_STORAGE_KEY
    );
    if (legacyScope !== null) {
      const parsed = parseStoredWorkspaceSelection(legacyScope);
      localStorage.removeItem(LEGACY_WORKSPACE_SCOPE_STORAGE_KEY);
      if (parsed) {
        persistWorkspaceSelection(parsed);
        return parsed;
      }
    }

    const cockpitLegacy = localStorage.getItem(
      LEGACY_COCKPIT_WORKSPACE_STORAGE_KEY
    );
    if (cockpitLegacy !== null) {
      const sanitized = sanitizeWorkspaceName(cockpitLegacy);
      localStorage.removeItem(LEGACY_COCKPIT_WORKSPACE_STORAGE_KEY);
      if (sanitized) {
        const selection = {
          kind: WorkspaceKind.workspace,
          workspace: sanitized,
        };
        persistWorkspaceSelection(selection);
        return selection;
      }
    }
  } catch {
    // Ignore storage access errors.
  }
  return defaultWorkspaceSelection();
}

export function persistWorkspaceSelection(selection: WorkspaceSelection): void {
  try {
    const sanitized = sanitizeWorkspaceSelection(selection);
    localStorage.setItem(WORKSPACE_STORAGE_KEY, JSON.stringify(sanitized));
    localStorage.removeItem(LEGACY_WORKSPACE_SCOPE_STORAGE_KEY);
    localStorage.removeItem(LEGACY_COCKPIT_WORKSPACE_STORAGE_KEY);
  } catch {
    // Ignore storage access errors.
  }
}

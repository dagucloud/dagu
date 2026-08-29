// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { SyncItemKind } from '@/api/v1/schema';

export type SyncKind = 'dag' | 'doc' | 'doc-asset' | 'file';

export const syncKindFilters: SyncKind[] = ['dag', 'doc', 'doc-asset', 'file'];

export const syncKindLabels: Record<
  SyncKind,
  {
    singular: string;
    plural: string;
    selectionSingular: string;
    selectionPlural: string;
    badge: string;
  }
> = {
  dag: {
    singular: 'DAG',
    plural: 'DAGs',
    selectionSingular: 'DAG',
    selectionPlural: 'DAGs',
    badge: 'dag',
  },
  doc: {
    singular: 'Wiki page',
    plural: 'Wiki pages',
    selectionSingular: 'Wiki page',
    selectionPlural: 'Wiki pages',
    badge: 'wiki',
  },
  'doc-asset': {
    singular: 'attachment',
    plural: 'Attachments',
    selectionSingular: 'attachment',
    selectionPlural: 'attachments',
    badge: 'file',
  },
  file: {
    singular: 'supporting file',
    plural: 'Supporting files',
    selectionSingular: 'supporting file',
    selectionPlural: 'supporting files',
    badge: 'file',
  },
};

export const syncKindBadgeClass: Partial<Record<SyncKind, string>> = {
  doc: 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400',
  'doc-asset': 'bg-sky-500/10 text-sky-600 dark:text-sky-400',
  file: 'bg-violet-500/10 text-violet-600 dark:text-violet-400',
};

export function createSyncKindCounts(): Record<SyncKind, number> {
  return {
    dag: 0,
    doc: 0,
    'doc-asset': 0,
    file: 0,
  };
}

export function parseSyncKind(value: string | null): SyncKind {
  if (value && syncKindFilters.includes(value as SyncKind)) {
    return value as SyncKind;
  }
  return 'dag';
}

export function normalizeSyncItemKind(kind: SyncItemKind): SyncKind {
  switch (kind) {
    case SyncItemKind.doc:
      return 'doc';
    case SyncItemKind.doc_asset:
      return 'doc-asset';
    case SyncItemKind.file:
      return 'file';
    default:
      return 'dag';
  }
}

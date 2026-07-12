// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ViewColumn } from '@/api/v1/schema';

export const DEFAULT_VIEW_COLUMNS: readonly ViewColumn[] = [
  ViewColumn.queued,
  ViewColumn.running,
  ViewColumn.review,
  ViewColumn.done,
  ViewColumn.failed,
];

export const VIEW_COLUMN_LABELS: Record<ViewColumn, string> = {
  [ViewColumn.queued]: 'Queued',
  [ViewColumn.running]: 'Running',
  [ViewColumn.review]: 'Review',
  [ViewColumn.done]: 'Done',
  [ViewColumn.failed]: 'Failed',
};

export interface ViewColumnSetting {
  column: ViewColumn;
  visible: boolean;
}

export function normalizeViewColumns(
  columns?: readonly ViewColumn[]
): ViewColumn[] {
  if (!columns?.length) {
    return [...DEFAULT_VIEW_COLUMNS];
  }

  const supported = new Set(DEFAULT_VIEW_COLUMNS);
  const normalized = columns.filter(
    (column, index) =>
      supported.has(column) && columns.indexOf(column) === index
  );
  return normalized.length > 0 ? normalized : [...DEFAULT_VIEW_COLUMNS];
}

export function createViewColumnSettings(
  visibleColumns?: readonly ViewColumn[]
): ViewColumnSetting[] {
  const visible = normalizeViewColumns(visibleColumns);
  return [
    ...visible.map((column) => ({ column, visible: true })),
    ...DEFAULT_VIEW_COLUMNS.filter((column) => !visible.includes(column)).map(
      (column) => ({ column, visible: false })
    ),
  ];
}

export function setViewColumnVisibility(
  settings: ViewColumnSetting[],
  column: ViewColumn,
  visible: boolean
): ViewColumnSetting[] {
  const setting = settings.find((item) => item.column === column);
  if (!setting || setting.visible === visible) {
    return settings;
  }

  const remaining = settings.filter((item) => item.column !== column);
  if (!visible) {
    const visibleCount = settings.filter((item) => item.visible).length;
    return visibleCount === 1
      ? settings
      : [...remaining, { column, visible: false }];
  }

  const firstHiddenIndex = remaining.findIndex((item) => !item.visible);
  const insertionIndex =
    firstHiddenIndex === -1 ? remaining.length : firstHiddenIndex;
  return [
    ...remaining.slice(0, insertionIndex),
    { column, visible: true },
    ...remaining.slice(insertionIndex),
  ];
}

export function moveVisibleViewColumn(
  settings: ViewColumnSetting[],
  column: ViewColumn,
  direction: -1 | 1
): ViewColumnSetting[] {
  const index = settings.findIndex((item) => item.column === column);
  if (index === -1 || !settings[index]!.visible) {
    return settings;
  }

  const target = index + direction;
  if (target < 0 || target >= settings.length || !settings[target]!.visible) {
    return settings;
  }

  const next = [...settings];
  [next[index], next[target]] = [next[target]!, next[index]!];
  return next;
}

// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ViewSortField, ViewSortOrder } from '@/api/v1/schema';

// The server reads the latest run of every matching workflow to sort by status
// or last run, so it applies those sorts only up to this many matching
// workflows and sorts by name above it. Keep in sync with maxRunSortDAGs in
// internal/service/frontend/api/v1/dags.go.
export const maxRunSortDAGs = 300;

export function isRunSortField(field?: string | null): boolean {
  return field === ViewSortField.status || field === ViewSortField.lastRun;
}

export type WorkflowFilterSet = {
  searchText: string;
  searchLabels: string[];
  activeOnly: boolean;
  sortField: ViewSortField;
  sortOrder: ViewSortOrder;
};

export type WorkflowFilterView = {
  id: string;
  name: string;
  pinned: boolean;
  filters: WorkflowFilterSet;
};

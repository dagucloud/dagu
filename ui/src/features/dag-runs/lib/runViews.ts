// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  RunDateMode,
  RunDatePreset,
  RunSpecificPeriod,
  ViewSpecType,
} from '@/api/v1/schema';
import type { View, ViewSpec } from '@/hooks/useViews';
import type { ViewScope } from '@/features/views/viewScope';

export type DAGRunsDateRangeMode = 'preset' | 'specific' | 'custom';
export type DAGRunsSpecificPeriod = 'date' | 'month' | 'year';

export type DAGRunsFilterSet = {
  searchText: string;
  dagRunId: string;
  status: string;
  labels: string[];
  fromDate?: string;
  toDate?: string;
  dateRangeMode: DAGRunsDateRangeMode;
  datePreset: string;
  specificPeriod: DAGRunsSpecificPeriod;
  specificValue: string;
};

export type DAGRunsFilterView = {
  id: string;
  name: string;
  pinned: boolean;
  filters: DAGRunsFilterSet;
};

export function dagRunsFilterSetFromView(view: View): DAGRunsFilterSet {
  return {
    searchText: view.dagName ?? '',
    dagRunId: view.dagRunId ?? '',
    status: view.runStatus ?? 'all',
    labels: view.labels ?? [],
    fromDate: view.fromDate ?? undefined,
    toDate: view.toDate ?? undefined,
    dateRangeMode: asDateRangeMode(view.dateMode),
    datePreset: view.datePreset ?? RunDatePreset.today,
    specificPeriod: asSpecificPeriod(view.specificPeriod),
    specificValue: view.specificValue ?? '',
  };
}

function asDateRangeMode(value: string | undefined): DAGRunsDateRangeMode {
  if (
    value === RunDateMode.preset ||
    value === RunDateMode.specific ||
    value === RunDateMode.custom
  ) {
    return value;
  }
  return RunDateMode.preset;
}

function asSpecificPeriod(value: string | undefined): DAGRunsSpecificPeriod {
  if (
    value === RunSpecificPeriod.date ||
    value === RunSpecificPeriod.month ||
    value === RunSpecificPeriod.year
  ) {
    return value;
  }
  return RunSpecificPeriod.date;
}

export function buildRunViewSpec(
  name: string,
  filters: DAGRunsFilterSet,
  isDefault: boolean,
  pinned: boolean,
  scope: ViewScope
): ViewSpec {
  return {
    name,
    type: ViewSpecType.run,
    workspace: scope.workspace,
    workspaceScope: scope.workspaceScope,
    labels: [...filters.labels],
    dagName: filters.searchText,
    dagRunId: filters.dagRunId,
    runStatus: filters.status,
    dateMode: filters.dateRangeMode as RunDateMode,
    datePreset: filters.datePreset as RunDatePreset,
    specificPeriod: filters.specificPeriod as RunSpecificPeriod,
    specificValue: filters.specificValue,
    fromDate: filters.fromDate,
    toDate: filters.toDate,
    intervalDays: 1,
    pinned,
    isDefault,
  };
}

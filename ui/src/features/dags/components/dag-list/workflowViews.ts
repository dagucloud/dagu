// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

export type WorkflowFilterSet = {
  searchText: string;
  searchLabels: string[];
  sortField: string;
  sortOrder: string;
};

export type WorkflowFilterView = {
  id: string;
  name: string;
  filters: WorkflowFilterSet;
};

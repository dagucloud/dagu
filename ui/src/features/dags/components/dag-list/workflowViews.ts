// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ViewSortField, ViewSortOrder } from '@/api/v1/schema';

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

// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React from 'react';

export const DAGContext = React.createContext<{
  refresh: () => void;
  name: string;
  fileName: string;
  forceEnqueue?: boolean;
  autoOpenStartModal?: boolean;
  /**
   * The host shows the run that a start begins, as the workflow page does:
   * no progress card for it.
   */
  showsStartedRun?: boolean;
  onEnqueue?: (
    params: string,
    dagRunId?: string,
    immediate?: boolean,
    profile?: string,
    noReuse?: boolean
  ) => string | void | Promise<string | void>;
}>({
  refresh: () => {
    return;
  },
  name: '',
  fileName: '',
  forceEnqueue: false,
  autoOpenStartModal: false,
});

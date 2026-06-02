// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React from 'react';

export const DAGContext = React.createContext<{
  refresh: () => void;
  name: string;
  fileName: string;
  forceEnqueue?: boolean;
  autoOpenStartModal?: boolean;
  onEnqueue?: (
    params: string,
    dagRunId?: string,
    immediate?: boolean,
    profile?: string
  ) => string | void | Promise<string | void>;
  onRunStarted?: (dagRunId: string) => void | Promise<void>;
}>({
  refresh: () => {
    return;
  },
  name: '',
  fileName: '',
  forceEnqueue: false,
  autoOpenStartModal: false,
});

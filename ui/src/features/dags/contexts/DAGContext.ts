// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React from 'react';
import type { StartRequest } from '../components/dag-execution/startRequest';

export const DAGContext = React.createContext<{
  refresh: () => void;
  name: string;
  fileName: string;
  forceEnqueue?: boolean;
  autoOpenStartModal?: boolean;
  onEnqueue?: (request: StartRequest) => string | void | Promise<string | void>;
}>({
  refresh: () => {
    return;
  },
  name: '',
  fileName: '',
  forceEnqueue: false,
  autoOpenStartModal: false,
});

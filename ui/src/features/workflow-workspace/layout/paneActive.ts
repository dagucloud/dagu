// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { createContext, useContext } from 'react';

/** Provided by each pane of the workspace; true outside any pane. */
export const PaneActiveContext = createContext(true);

/** false inside the inactive pane, true elsewhere (and outside the panes).
 *  Document-level key handlers inside a pane return early when false. */
export function useModePaneActive(): boolean {
  return useContext(PaneActiveContext);
}

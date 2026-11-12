// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { createContext } from 'react';

/**
 * The place in the header of the dock where the run timeline puts its
 * controls, beside the Maximize button. undefined: there is no dock header
 * (the timeline draws its controls in its own row). null: the header is not
 * ready.
 */
export const DockHeaderToolsContext = createContext<
  HTMLElement | null | undefined
>(undefined);

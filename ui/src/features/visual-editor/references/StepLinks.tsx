// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { createContext, useContext } from 'react';

/**
 * What a host gives to the names of the steps in the Upstream data column so
 * that a name is a link: it opens the step in the node view.
 */
export interface StepLinks {
  /** Selects the step, by server name, and opens its node view. */
  open(step: string): void;
}

export const StepLinksContext = createContext<StepLinks | null>(null);

/** The step links of the host; null for a host that has none. */
export function useStepLinks(): StepLinks | null {
  return useContext(StepLinksContext);
}

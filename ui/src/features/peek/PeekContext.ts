// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { createContext } from 'react';
import type { PeekTarget } from './peekParams';

/** What a PeekHost gives the lists below it. */
export interface PeekController {
  /** Shows the target in the peek, as a new history entry. */
  open(target: PeekTarget): void;
  /** True while a peek is open over the list. */
  isOpen: boolean;
  /** Sets the targets of the rows of the list, which the peek steps through. */
  setSequence?(targets: readonly PeekTarget[]): void;
  /** The row that the peek shows, or showed last: the list marks it. */
  shown?: PeekTarget | null;
}

/** Null without a PeekHost above: the rows then open the full page. */
export const PeekContext = createContext<PeekController | null>(null);

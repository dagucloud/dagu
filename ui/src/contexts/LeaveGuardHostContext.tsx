// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { createContext, useContext } from 'react';

/** Runs leave() at once, or once the user accepts that a draft is lost. */
export type LeaveGuard = (leave: () => void) => void;

/**
 * A host that can take its page away, such as the workflow overlay. The page
 * registers a guard while it is shown, and the host asks it before it
 * leaves, so that the page stays in place while the question is open.
 */
export interface LeaveGuardHost {
  /** Sets the guard; the returned function removes it. */
  register(guard: LeaveGuard): () => void;
}

const LeaveGuardHostContext = createContext<LeaveGuardHost | null>(null);

export const LeaveGuardHostProvider = LeaveGuardHostContext.Provider;

/** Null outside a host that takes its page away. */
export function useLeaveGuardHost(): LeaveGuardHost | null {
  return useContext(LeaveGuardHostContext);
}

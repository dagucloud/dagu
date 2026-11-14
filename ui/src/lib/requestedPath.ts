// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { createPath, type Path } from 'react-router-dom';

/**
 * Router state of the sign-in and setup pages: the page a visitor asked for
 * before being sent there.
 */
export type RequestedPathState = { from?: Partial<Path> };

/**
 * Returns the page that state says a visitor asked for, with its query and
 * fragment, or '/' without one. Only a path within the app is returned, so
 * that no state can send the visitor to another site.
 */
export function requestedPath(state: unknown): string {
  const from = (state as RequestedPathState | null)?.from;
  const pathname = from?.pathname;
  if (
    typeof pathname !== 'string' ||
    !pathname.startsWith('/') ||
    pathname.startsWith('//') ||
    pathname.startsWith('/\\')
  ) {
    return '/';
  }
  return createPath({ pathname, search: from?.search, hash: from?.hash });
}

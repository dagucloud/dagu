// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ReactElement } from 'react';
import { Navigate, useLocation } from 'react-router-dom';

/** The AI page was the Toolbox. A link to /toolbox/... goes to /ai/.... */
export function LegacyToolboxRedirect(): ReactElement {
  const location = useLocation();
  const suffix = location.pathname.replace(/^\/toolbox/, '');
  return (
    <Navigate to={`/ai${suffix}${location.search}${location.hash}`} replace />
  );
}

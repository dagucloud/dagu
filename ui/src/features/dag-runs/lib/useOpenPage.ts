// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback } from 'react';
import { useNavigate } from 'react-router-dom';
import { useConfig } from '@/contexts/ConfigContext';

type ClickModifiers = { ctrlKey?: boolean; metaKey?: boolean };

/**
 * Opens an app URL from a row of a list: a plain click navigates, a Ctrl or
 * Cmd click opens a new tab, as the name link of the row does.
 */
export function useOpenPage(): (to: string, event?: ClickModifiers) => void {
  const navigate = useNavigate();
  const config = useConfig();
  return useCallback(
    (to, event) => {
      if (event?.ctrlKey || event?.metaKey) {
        window.open(`${config.basePath || ''}${to}`, '_blank', 'noopener');
        return;
      }
      navigate(to);
    },
    [config, navigate]
  );
}

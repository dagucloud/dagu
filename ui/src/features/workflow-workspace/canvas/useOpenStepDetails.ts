// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback } from 'react';
import { useWorkspaceUrl } from '../state/workspaceUrl';
import { useWorkspaceEditor } from './WorkspaceEditorContext';

/**
 * Opens the node details view of a step on its YAML, never on its form, and
 * ?step with ?ndv=expanded (replace). Without a bound editor only the URL
 * changes. With `form: false` the step selects as it does for a click on its
 * card.
 */
export function useOpenStepDetails({ form = true }: { form?: boolean } = {}): (
  step: string
) => void {
  const { handle } = useWorkspaceEditor();
  const [, setUrl] = useWorkspaceUrl();
  return useCallback(
    (step: string) => {
      if (handle) {
        if (!form || !handle.openYaml(step)) handle.revealStep(step);
      }
      // After the editor: its own URL writes come from another hook, so
      // this write carries both keys.
      setUrl({ step, ndv: 'expanded' }, { replace: true });
    },
    [form, handle, setUrl]
  );
}

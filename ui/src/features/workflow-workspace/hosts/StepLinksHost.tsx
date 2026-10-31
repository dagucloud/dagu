// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useMemo, type ReactNode } from 'react';
import {
  StepLinksContext,
  type StepLinks,
} from '@/features/visual-editor/references/StepLinks';
import { useOpenStepDetails } from '../canvas/useOpenStepDetails';

/**
 * Gives the names of the previous steps in the Upstream data column a click
 * that opens the step in the node view (?step and ?ndv=expanded; the other
 * keys of the URL stay, the tab of the view stays). It sits inside the editor
 * provider, as the dock does.
 */
export function StepLinksHost({ children }: { children: ReactNode }) {
  // The step view keeps its tab, the YAML by default, as for any step.
  const open = useOpenStepDetails({ form: false });
  const links = useMemo<StepLinks>(() => ({ open }), [open]);
  return (
    <StepLinksContext.Provider value={links}>
      {children}
    </StepLinksContext.Provider>
  );
}

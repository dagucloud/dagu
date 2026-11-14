// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useEffect, useState } from 'react';
import type { VisualEditorHandle } from '@/features/visual-editor/contracts';

/** What the node view of a step shows in its center. */
export type StepViewMode = 'yaml' | 'form';

const STORAGE_KEY = 'dagu.stepView.mode';

/** The last mode of a step view in this session; the YAML when none. */
export function readStepViewMode(): StepViewMode {
  try {
    return sessionStorage.getItem(STORAGE_KEY) === 'form' ? 'form' : 'yaml';
  } catch {
    return 'yaml';
  }
}

function writeStepViewMode(mode: StepViewMode): void {
  try {
    sessionStorage.setItem(STORAGE_KEY, mode);
  } catch {
    // The mode is a convenience: no storage, no memory of it.
  }
}

/**
 * Opens the node view of a step in the last mode of the session: its form
 * when that was the last mode and the step has one, else its YAML. False when
 * the editor has no such step.
 */
export function openStepInLastMode(
  handle: Pick<VisualEditorHandle, 'openForm' | 'openYaml'>,
  step: string
): boolean {
  if (readStepViewMode() === 'form' && handle.openForm(step)) return true;
  return handle.openYaml(step);
}

/**
 * The form flag of the node view of a step. While `open` is true, each change
 * is the last mode of the session; a view that opens again, or a host that
 * mounts again, starts in that mode. A view that is not open (the YAML of the
 * whole file) starts on the text.
 */
export function useStepViewForm(
  open: boolean
): [boolean, (form: boolean) => void] {
  const [form, setForm] = useState(() => open && readStepViewMode() === 'form');
  useEffect(() => {
    if (open) writeStepViewMode(form ? 'form' : 'yaml');
  }, [open, form]);
  const set = useCallback((next: boolean) => setForm(next), []);
  return [form, set];
}

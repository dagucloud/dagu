// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect, useRef, type RefObject } from 'react';
import { openStepInLastMode } from '@/features/dags/components/dag-editor/stepViewMode';
import type { VisualEditorHandle } from '@/features/visual-editor/contracts';
import { escFree } from './escFree';
import { useEscClearsStep } from './useEscClearsStep';

/**
 * The keys of a graph zone that shows a step view: Esc closes the open step
 * view when no dialog or menu is above it. With no panel or step view open,
 * Esc clears the selected step.
 */
export function useStepViewKeys({
  zoneRef,
  active,
  hasStep,
  viewOpen,
  panelOpen,
  closeView,
  clearStep,
}: {
  zoneRef: RefObject<HTMLElement | null>;
  /** The pane is the visible one. */
  active: boolean;
  hasStep: boolean;
  viewOpen: boolean;
  /** A right panel is open: it owns Esc. */
  panelOpen: boolean;
  closeView(): void;
  clearStep(): void;
}): void {
  const closeRef = useRef(closeView);
  closeRef.current = closeView;
  useEffect(() => {
    if (!active || !viewOpen) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (!escFree(event)) return;
      event.preventDefault();
      closeRef.current();
    };
    document.addEventListener('keydown', onKeyDown);
    return () => document.removeEventListener('keydown', onKeyDown);
  }, [active, viewOpen]);

  useEscClearsStep({
    zoneRef,
    active,
    hasStep,
    layerOpen: viewOpen || panelOpen,
    clear: clearStep,
  });
}

/** The step (and the step view) that a link names. */
export interface StepDeepLink {
  step: string;
  ndv: string | null;
}

/**
 * Applies the deep link of the first render once the editor has loaded: the
 * step view opens on the step, or the canvas reveals the step. `reveal` is
 * false for a canvas that has no step to scroll to.
 */
export function useDeepLinkReveal(
  handle: VisualEditorHandle | null,
  link: StepDeepLink | null,
  reveal: boolean
): void {
  const pending = useRef(link);
  useEffect(() => {
    const target = pending.current;
    if (!handle || !target) return;
    pending.current = null;
    // The step view opens in the mode that the user used last, the YAML
    // at first.
    if (
      target.ndv &&
      (target.ndv === 'expanded'
        ? openStepInLastMode(handle, target.step)
        : handle.openForm(target.step))
    ) {
      return;
    }
    if (reveal) handle.revealStep(target.step);
  }, [handle, reveal]);
}

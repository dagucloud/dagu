// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type RefObject,
} from 'react';
import { escFree } from './escFree';

/** The step panel in the graph zone; StepPanel puts this mark on its root. */
const STEP_PANEL_SELECTOR = '[data-step-panel]';
const CANVAS_SELECTOR = '[data-testid="ve-canvas"]';

export interface StepPanelState {
  /** The panel shows: it is open, a step is selected and nothing covers it. */
  shown: boolean;
  /** Opens the panel, for a click on a step of the canvas. */
  show(): void;
  /** Closes the panel; the step stays selected. */
  close(): void;
}

/**
 * The open state of the step panel of the editor canvas. It is no URL key:
 * the panel shows the step of ?step.
 *
 * A click on a step opens it, and so does a link that names a step but no
 * node view and no Run panel tab. While open, it follows ?step. It closes on
 * close() (its button, Esc), when ?step goes, and when a right panel, the
 * node view or another view of the zone covers it. A selection from
 * elsewhere, such as the Run panel or the history, never opens it.
 *
 * Esc closes the shown panel while the focus is in the zone (the canvas or
 * the panel) or on the page, unless a dialog, a menu or a text field takes
 * the key. A close from inside the panel gives the focus to the canvas.
 */
export function useStepPanel({
  zoneRef,
  step,
  covered,
  linked,
  active,
}: {
  zoneRef: RefObject<HTMLElement | null>;
  /** ?step: the server name of the selected step. */
  step: string | null;
  /** A right panel, the node view or another view of the zone is open. */
  covered: boolean;
  /** At mount: the URL is a link to the panel of the step. */
  linked: boolean;
  /** The pane is the visible one: Esc counts. */
  active: boolean;
}): StepPanelState {
  const [open, setOpen] = useState(linked);

  // What comes over the panel takes its place, and a cleared step leaves it
  // nothing to show: a later selection does not bring it back. A click while
  // something covers the zone (a launcher panel that the click closes) keeps
  // the panel open, to show once the zone is free.
  const wasCovered = useRef(covered);
  useEffect(() => {
    const comesOver = covered && !wasCovered.current;
    wasCovered.current = covered;
    if (comesOver || step === null) setOpen(false);
  }, [covered, step]);

  const show = useCallback(() => setOpen(true), []);
  const close = useCallback(() => {
    const zone = zoneRef.current;
    const focus = document.activeElement;
    const inPanel =
      focus !== null &&
      !!zone?.querySelector(STEP_PANEL_SELECTOR)?.contains(focus);
    setOpen(false);
    if (inPanel) zone?.querySelector<HTMLElement>(CANVAS_SELECTOR)?.focus();
  }, [zoneRef]);

  const shown = open && step !== null && !covered;
  const closeRef = useRef(close);
  closeRef.current = close;
  useEffect(() => {
    if (!active || !shown) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (!escFree(event)) return;
      const target = event.target as Node | null;
      const onPage =
        target === document.body || target === document.documentElement;
      if (!onPage && !(target && zoneRef.current?.contains(target))) return;
      event.preventDefault();
      closeRef.current();
    };
    document.addEventListener('keydown', onKeyDown);
    return () => document.removeEventListener('keydown', onKeyDown);
  }, [active, shown, zoneRef]);

  return { shown, show, close };
}

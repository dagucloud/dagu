// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type RefObject,
} from 'react';

const PANEL_SELECTOR = '[data-panel-shell="right"]';
const CANVAS_SELECTOR = '[data-testid="ve-canvas"]';
/** Time in ms after a change of the trigger during which a late panel is looked for. */
export const PANEL_WATCH_MS = 1500;

export interface RightPanelClearance {
  /** Width in px of the right panel over the canvas; 0 when none covers it. */
  overlap: number;
  /** Width in px of the zone that the panel leaves free; null without panel. */
  free: number | null;
}

const NONE: RightPanelClearance = { overlap: 0, free: null };

/** Measures the right panel in `zone` against the canvas and the zone. */
export function measureRightPanel(zone: HTMLElement): RightPanelClearance {
  const panel = zone.querySelector<HTMLElement>(PANEL_SELECTOR);
  if (!panel) return NONE;
  const panelBox = panel.getBoundingClientRect();
  if (panelBox.width === 0) return NONE;
  const zoneBox = zone.getBoundingClientRect();
  const canvas = zone.querySelector<HTMLElement>(CANVAS_SELECTOR);
  const canvasRight = canvas?.getBoundingClientRect().right ?? zoneBox.right;
  return {
    // A canvas that the host shrank ends at the panel: nothing is covered.
    overlap: Math.max(0, Math.round(canvasRight - panelBox.left)),
    free: Math.max(0, Math.round(panelBox.left - zoneBox.left)),
  };
}

/**
 * Follows the right panel of the graph zone (Workflow, YAML, Add step, Step
 * details): how much of the canvas it covers and how much of the zone stays
 * free. The panel is absolute in the zone and may cover the canvas or sit
 * in a padding that the editor keeps for it.
 */
export function useRightPanelClearance(
  zoneRef: RefObject<HTMLElement | null>,
  /** Changes when a panel opens or closes. */
  trigger: unknown
): RightPanelClearance {
  const [clearance, setClearance] = useState(NONE);
  // The panel and the canvas whose size the observer follows.
  const observed = useRef<Element[]>([]);
  const resizeObserver = useRef<ResizeObserver | null>(null);

  const measure = useCallback(() => {
    const zone = zoneRef.current;
    if (!zone) return;
    const next = measureRightPanel(zone);
    setClearance((current) =>
      current.overlap === next.overlap && current.free === next.free
        ? current
        : next
    );
    // The panel can change its width alone, by its resize handle, and the host
    // can change the width of the canvas.
    const followed = [
      zone.querySelector(PANEL_SELECTOR),
      zone.querySelector(CANVAS_SELECTOR),
    ].filter((element): element is Element => element !== null);
    const before = observed.current;
    if (
      followed.length !== before.length ||
      followed.some((element, index) => element !== before[index])
    ) {
      before.forEach((element) => resizeObserver.current?.unobserve(element));
      observed.current = followed;
      followed.forEach((element) => resizeObserver.current?.observe(element));
    }
  }, [zoneRef]);

  const frame = useRef(0);
  const schedule = useCallback(() => {
    cancelAnimationFrame(frame.current);
    frame.current = requestAnimationFrame(measure);
  }, [measure]);

  // The size of the zone and the size of the panel are the only things that
  // change the answer, apart from a panel that opens or closes (the trigger).
  useEffect(() => {
    const zone = zoneRef.current;
    if (!zone) return;
    if (typeof ResizeObserver !== 'undefined') {
      resizeObserver.current = new ResizeObserver(schedule);
      resizeObserver.current.observe(zone);
    }
    window.addEventListener('resize', schedule);
    measure();
    return () => {
      cancelAnimationFrame(frame.current);
      resizeObserver.current?.disconnect();
      resizeObserver.current = null;
      observed.current = [];
      window.removeEventListener('resize', schedule);
    };
  }, [zoneRef, measure, schedule]);

  // A panel mounts after its claim: after a change of the trigger, the zone is
  // watched until the panel is there, for a short time. A zone that nobody
  // changes is not watched.
  useEffect(() => {
    const zone = zoneRef.current;
    measure();
    if (
      !zone ||
      typeof MutationObserver === 'undefined' ||
      trigger === undefined
    ) {
      return;
    }
    const stop = () => {
      watcher.disconnect();
      clearTimeout(timer);
    };
    // Only a panel that is not yet followed asks for a measure.
    const watcher = new MutationObserver(() => {
      const panel = zone.querySelector(PANEL_SELECTOR);
      const known = observed.current.find((element) =>
        element.matches(PANEL_SELECTOR)
      );
      if (panel !== (known ?? null)) {
        schedule();
      }
    });
    const timer = setTimeout(stop, PANEL_WATCH_MS);
    watcher.observe(zone, { childList: true, subtree: true });
    return stop;
  }, [trigger, measure, schedule, zoneRef]);

  return clearance;
}

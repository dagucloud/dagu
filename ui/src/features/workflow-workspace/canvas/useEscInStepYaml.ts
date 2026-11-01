// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect, useRef } from 'react';
import { ESC_OWNER_SELECTOR } from './escFree';

/** The YAML editor inside the expanded node view. */
const STEP_YAML_SELECTOR = '[data-testid="ndv"] .monaco-editor';

/** Widgets of the text editor that take Esc to close themselves. */
const EDITOR_WIDGETS = [
  '.suggest-widget.visible',
  '.parameter-hints-widget.visible',
  '.find-widget.visible',
].join(', ');

/**
 * Esc in the YAML editor of the node view closes the view. The text editor
 * marks every Esc as handled, so the key is read before the editor gets it
 * (capture phase), and an open widget of the editor (suggestions, find) keeps
 * it. The other layers ignore a key that a text field receives, so this hook
 * covers the editor of the view only.
 */
export function useEscInStepYaml({
  active,
  viewOpen,
  closeView,
}: {
  /** The pane is the visible one. */
  active: boolean;
  viewOpen: boolean;
  closeView(): void;
}): void {
  const closeRef = useRef(closeView);
  useEffect(() => {
    closeRef.current = closeView;
  });
  useEffect(() => {
    if (!active || !viewOpen) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape' || event.defaultPrevented) return;
      if (event.shiftKey || event.altKey || event.ctrlKey || event.metaKey) {
        return;
      }
      const target = event.target;
      if (!(target instanceof Element)) return;
      if (target.closest(STEP_YAML_SELECTOR) === null) return;
      if (document.querySelector(ESC_OWNER_SELECTOR) !== null) return;
      if (document.querySelector(EDITOR_WIDGETS) !== null) return;
      event.preventDefault();
      event.stopPropagation();
      closeRef.current();
    };
    document.addEventListener('keydown', onKeyDown, true);
    return () => document.removeEventListener('keydown', onKeyDown, true);
  }, [active, viewOpen]);
}

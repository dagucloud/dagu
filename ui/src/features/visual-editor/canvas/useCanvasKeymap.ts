// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect, useRef, type RefObject } from 'react';

/** Actions of the canvas keyboard shortcuts. */
export type CanvasKeyAction =
  | 'delete'
  | 'open'
  | 'clear'
  | 'palette'
  | 'fit'
  | 'menu'
  | 'rename'
  | 'help'
  | 'copy'
  | 'duplicate'
  | 'zoomIn'
  | 'zoomOut'
  | 'zoomReset'
  | 'navUp'
  | 'navDown'
  | 'navLeft'
  | 'navRight';

/**
 * One key of a binding. `shift` undefined accepts both states. `mod` asks
 * for Ctrl or Cmd; a chord without it refuses Ctrl and Cmd.
 */
export interface CanvasKeyChord {
  key: string;
  shift?: boolean;
  mod?: boolean;
}

/**
 * An action of the key table: a key action, the paste event, or the history
 * keys that the editor owns (the table lists them for the hints).
 */
export type CanvasKeyTableAction = CanvasKeyAction | 'paste' | 'undo' | 'redo';

export interface CanvasKeyBinding {
  action: CanvasKeyTableAction;
  chords: readonly CanvasKeyChord[];
  /** Key names as the key help and the menu rows show them. */
  keys: readonly string[];
  /** English i18n source text of what the key does. */
  label: string;
}

/** Every bound key of the canvas. The key help and the menus read it. */
export const CANVAS_KEY_TABLE: readonly CanvasKeyBinding[] = [
  {
    action: 'open',
    chords: [{ key: 'Enter' }],
    keys: ['Enter'],
    label: 'Open the selected step',
  },
  {
    action: 'rename',
    chords: [{ key: 'F2' }],
    keys: ['F2'],
    label: 'Rename the selected step',
  },
  {
    action: 'delete',
    chords: [{ key: 'Delete' }, { key: 'Backspace' }],
    keys: ['Delete', 'Backspace'],
    label: 'Delete the selection',
  },
  {
    action: 'palette',
    chords: [{ key: 'n' }, { key: 'N' }],
    keys: ['N'],
    label: 'Add a step',
  },
  {
    action: 'fit',
    chords: [{ key: 'f' }, { key: 'F' }, { key: '0' }],
    keys: ['F', '0'],
    label: 'Fit the view',
  },
  {
    action: 'clear',
    chords: [{ key: 'Escape' }],
    keys: ['Esc'],
    label: 'Clear the selection',
  },
  {
    action: 'menu',
    chords: [{ key: 'F10', shift: true }, { key: 'ContextMenu' }],
    keys: ['Shift+F10', 'Menu'],
    label: 'Open the menu of the selection',
  },
  {
    action: 'help',
    chords: [{ key: '?' }],
    keys: ['Shift+?'],
    label: 'Show the keyboard shortcuts',
  },
  {
    action: 'copy',
    chords: [
      { key: 'c', mod: true, shift: false },
      { key: 'C', mod: true, shift: false },
    ],
    keys: ['Ctrl/Cmd+C'],
    label: 'Copy the selected step',
  },
  // The paste event of the canvas gives the text; no key event does.
  {
    action: 'paste',
    chords: [],
    keys: ['Ctrl/Cmd+V'],
    label: 'Paste steps',
  },
  {
    action: 'duplicate',
    chords: [
      { key: 'd', mod: true, shift: false },
      { key: 'D', mod: true, shift: false },
    ],
    keys: ['Ctrl/Cmd+D'],
    label: 'Duplicate the selected step',
  },
  // The editor handles Undo and Redo on its own key listener; the canvas
  // keymap never maps them.
  {
    action: 'undo',
    chords: [],
    keys: ['Ctrl/Cmd+Z'],
    label: 'Undo the last change',
  },
  {
    action: 'redo',
    chords: [],
    keys: ['Ctrl/Cmd+Shift+Z'],
    label: 'Redo the last change',
  },
  {
    action: 'zoomIn',
    chords: [{ key: '+' }, { key: '=' }],
    keys: ['+'],
    label: 'Zoom in',
  },
  {
    action: 'zoomOut',
    chords: [{ key: '-' }],
    keys: ['-'],
    label: 'Zoom out',
  },
  {
    action: 'zoomReset',
    chords: [{ key: '1' }],
    keys: ['1'],
    label: 'Zoom to 100%',
  },
  {
    action: 'navUp',
    chords: [{ key: 'ArrowUp', shift: false }],
    keys: ['↑'],
    label: 'Select the step above',
  },
  {
    action: 'navDown',
    chords: [{ key: 'ArrowDown', shift: false }],
    keys: ['↓'],
    label: 'Select the step below',
  },
  {
    action: 'navLeft',
    chords: [{ key: 'ArrowLeft', shift: false }],
    keys: ['←'],
    label: 'Select the step on the left',
  },
  {
    action: 'navRight',
    chords: [{ key: 'ArrowRight', shift: false }],
    keys: ['→'],
    label: 'Select the step on the right',
  },
];

/**
 * Display text of the first key bound to the action, or '' when none is. It
 * is the one source of the key text in menus, tooltips and the key help.
 */
export function canvasShortcut(action: CanvasKeyTableAction): string {
  return (
    CANVAS_KEY_TABLE.find((binding) => binding.action === action)?.keys[0] ?? ''
  );
}

const TEXT_ENTRY_SELECTOR = [
  'input',
  'textarea',
  'select',
  '[contenteditable=""]',
  '[contenteditable="true"]',
  '.monaco-editor',
].join(',');

/**
 * Tells if the key event target takes text: an input, a textarea, a select,
 * a contenteditable element or a Monaco editor. Shortcuts stay off there.
 */
export function isTextEntryTarget(target: EventTarget | null): boolean {
  if (!(target instanceof Element)) {
    return false;
  }
  return target.closest(TEXT_ENTRY_SELECTOR) !== null;
}

const CONTROL_SELECTOR = 'button, a[href], [role="button"], [role="option"]';

function chordMatches(
  chord: CanvasKeyChord,
  event: { key: string; shiftKey?: boolean; mod: boolean }
): boolean {
  return (
    chord.key === event.key &&
    (chord.mod ?? false) === event.mod &&
    (chord.shift === undefined || chord.shift === (event.shiftKey ?? false))
  );
}

/** Maps a key event to a canvas action, or null when no shortcut applies. */
export function canvasKeyAction(
  event: Pick<
    KeyboardEvent,
    'key' | 'ctrlKey' | 'metaKey' | 'altKey' | 'target' | 'defaultPrevented'
  > &
    Partial<Pick<KeyboardEvent, 'shiftKey'>>
): CanvasKeyAction | null {
  if (event.defaultPrevented || event.altKey) {
    return null;
  }
  if (isTextEntryTarget(event.target)) {
    return null;
  }
  // A focused button keeps Enter for its own click.
  if (
    event.key === 'Enter' &&
    event.target instanceof Element &&
    event.target.closest(CONTROL_SELECTOR) !== null
  ) {
    return null;
  }
  const pressed = {
    key: event.key,
    shiftKey: event.shiftKey,
    mod: event.ctrlKey || event.metaKey,
  };
  const binding = CANVAS_KEY_TABLE.find((candidate) =>
    candidate.chords.some((chord) => chordMatches(chord, pressed))
  );
  const action = binding?.action;
  return action === undefined ||
    action === 'paste' ||
    action === 'undo' ||
    action === 'redo'
    ? null
    : action;
}

/** Handlers of the key actions. `paste` gets the text of a paste event. */
export type CanvasKeyHandlers = Partial<Record<CanvasKeyAction, () => void>> & {
  paste?(text: string): void;
};

/**
 * Binds the canvas shortcuts to the element of `ref`, so that they apply
 * only while the focus is inside the canvas. A key with a handler is
 * consumed with `preventDefault`. A paste event gives its plain text to
 * `paste`. Text fields keep their own keys and paste.
 */
export function useCanvasKeymap(
  ref: RefObject<HTMLElement | null>,
  handlers: CanvasKeyHandlers,
  enabled = true
): void {
  const handlersRef = useRef(handlers);
  useEffect(() => {
    handlersRef.current = handlers;
  });

  useEffect(() => {
    const element = ref.current;
    if (!element || !enabled) {
      return;
    }
    const onKeyDown = (event: KeyboardEvent) => {
      const action = canvasKeyAction(event);
      const handler = action ? handlersRef.current[action] : undefined;
      if (handler) {
        event.preventDefault();
        handler();
      }
    };
    const onPaste = (event: ClipboardEvent) => {
      const paste = handlersRef.current.paste;
      if (!paste || event.defaultPrevented) {
        return;
      }
      if (isTextEntryTarget(event.target)) {
        return;
      }
      const text = event.clipboardData?.getData('text/plain') ?? '';
      if (text.trim() === '') {
        return;
      }
      event.preventDefault();
      paste(text);
    };
    element.addEventListener('keydown', onKeyDown);
    element.addEventListener('paste', onPaste);
    return () => {
      element.removeEventListener('keydown', onKeyDown);
      element.removeEventListener('paste', onPaste);
    };
  }, [ref, enabled]);
}

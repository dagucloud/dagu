// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useMemo, useRef, type FocusEvent } from 'react';

export type TextField = HTMLInputElement | HTMLTextAreaElement;

const TEXT_INPUT_TYPES = new Set(['', 'text', 'search', 'url']);

function isTextField(target: EventTarget | null): target is TextField {
  if (target instanceof HTMLTextAreaElement) {
    return !target.readOnly && !target.disabled;
  }
  return (
    target instanceof HTMLInputElement &&
    TEXT_INPUT_TYPES.has(target.type) &&
    !target.readOnly &&
    !target.disabled
  );
}

export interface LastFocusedField {
  /** Give to `onFocus` of the container; React focus events bubble. */
  onFocus(event: FocusEvent<HTMLElement>): void;
  /**
   * The last text field that had the focus, while it is in the document and
   * can be edited.
   */
  get(): TextField | null;
  /** Forgets the field, as when the form shows another step. */
  reset(): void;
}

/** Remembers the last text field that had the focus inside a container. */
export function useLastFocusedField(): LastFocusedField {
  const last = useRef<TextField | null>(null);

  const onFocus = useCallback((event: FocusEvent<HTMLElement>) => {
    if (isTextField(event.target)) {
      last.current = event.target;
    }
  }, []);

  const get = useCallback(() => {
    const field = last.current;
    return field?.isConnected && isTextField(field) ? field : null;
  }, []);

  const reset = useCallback(() => {
    last.current = null;
  }, []);

  return useMemo(() => ({ onFocus, get, reset }), [onFocus, get, reset]);
}

/**
 * Puts `token` at the selection of `field` and tells React about the new
 * value with an input event, as if the user typed it. The caret goes after
 * the token.
 */
export function insertIntoField(field: TextField, token: string): void {
  const start = field.selectionStart ?? field.value.length;
  const end = field.selectionEnd ?? start;
  const next = field.value.slice(0, start) + token + field.value.slice(end);
  const prototype =
    field instanceof HTMLTextAreaElement
      ? HTMLTextAreaElement.prototype
      : HTMLInputElement.prototype;
  // React tracks the value that it set; the native setter bypasses that, so
  // the input event reaches the onChange handler.
  Object.getOwnPropertyDescriptor(prototype, 'value')?.set?.call(field, next);
  field.dispatchEvent(new Event('input', { bubbles: true }));
  field.focus();
  const caret = start + token.length;
  field.setSelectionRange(caret, caret);
}

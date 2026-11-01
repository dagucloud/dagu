// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Shared by MonacoField and its lazy editor, so that neither imports the other.

export interface MonacoFieldProps {
  value: string;
  language: string;
  readOnly: boolean;
  /** Editor height in lines. */
  lines?: number;
  ariaLabel: string;
  /** Called with the text of the editor at each change. */
  onChange?(text: string): void;
  /** Called on blur when the text differs from `value`. */
  onCommit?(text: string): void;
}

/** What the frame of a field gives to the editor that loads in it. */
export interface MonacoFieldEditorProps extends MonacoFieldProps {
  /** True when the focus was on the empty frame: the editor takes it on mount. */
  focusRequest?: { current: boolean };
}

/** Height of the editor of a field of `lines` lines. */
export function fieldEditorHeight(lines: number): string {
  return `${lines * 19 + 8}px`;
}

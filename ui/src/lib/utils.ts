// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { clsx, type ClassValue } from 'clsx';
import { twMerge } from 'tailwind-merge';

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

/**
 * Parses a label string into its key and value components.
 * Supports both key-only labels ("production") and key=value labels ("env=prod").
 * For labels with multiple '=' characters, only the first '=' is used as delimiter.
 */
export function parseLabelParts(label: string): {
  key: string;
  value: string | null;
} {
  const eqIndex = label.indexOf('=');
  if (eqIndex === -1) {
    return { key: label, value: null };
  }
  return {
    key: label.slice(0, eqIndex),
    value: label.slice(eqIndex + 1),
  };
}

export const parseTagParts = parseLabelParts;

/**
 * Converts a step name to a deterministic Mermaid node ID by encoding every
 * code point and adding a safe prefix. This avoids reserved Mermaid words,
 * syntax-sensitive leading characters, and collisions between raw and encoded
 * step names.
 */
export function toMermaidNodeId(stepName: string): string {
  const encoded = Array.from(stepName)
    .map((ch) => ch.codePointAt(0)!.toString(16))
    .join('_');
  return `node_${encoded || 'empty'}`;
}

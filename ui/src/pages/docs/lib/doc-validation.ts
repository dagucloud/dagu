// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

export const DOC_PATH_PATTERN =
  /^[a-zA-Z0-9_][a-zA-Z0-9_. -]*(\/[a-zA-Z0-9_][a-zA-Z0-9_. -]*)*$/;

const MAX_DOC_PATH_LENGTH = 252;
const WINDOWS_RESERVED_SEGMENT =
  /^(con|prn|aux|nul|com[1-9]|lpt[1-9])(?:\..*)?$/i;

export function validateDocPath(path: string): {
  isValid: boolean;
  error?: string;
} {
  const trimmed = path.trim();
  if (!trimmed) {
    return { isValid: false, error: 'Path is required' };
  }
  if (trimmed.length > MAX_DOC_PATH_LENGTH) {
    return {
      isValid: false,
      error: `Path must be ${MAX_DOC_PATH_LENGTH} characters or fewer`,
    };
  }
  if (trimmed.toLowerCase().endsWith('.md')) {
    return {
      isValid: false,
      error: 'Path should not include the .md extension.',
    };
  }
  if (!DOC_PATH_PATTERN.test(trimmed)) {
    return {
      isValid: false,
      error:
        'Invalid path. Use letters, numbers, underscores, dots, hyphens, and spaces. Use / for directories.',
    };
  }
  const segments = trimmed.split('/');
  if (segments.some((segment) => /[ .]$/.test(segment))) {
    return {
      isValid: false,
      error: 'Path segments cannot end with a space or dot.',
    };
  }
  if (segments.some((segment) => WINDOWS_RESERVED_SEGMENT.test(segment))) {
    return {
      isValid: false,
      error: 'Path segments cannot use reserved device names.',
    };
  }
  return { isValid: true };
}

// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

export const DOC_PATH_PATTERN =
  /^[a-zA-Z0-9_][a-zA-Z0-9_. -]*(\/[a-zA-Z0-9_][a-zA-Z0-9_. -]*)*$/;

export function validateDocPath(path: string): {
  isValid: boolean;
  error?: string;
} {
  const trimmed = path.trim();
  if (!trimmed) {
    return { isValid: false, error: 'Path is required' };
  }
  if (path.length > 252) {
    return { isValid: false, error: 'Path must be 252 characters or fewer' };
  }
  if (path.toLowerCase().endsWith('.md')) {
    return {
      isValid: false,
      error: 'Path should not include the .md extension.',
    };
  }
  if (!DOC_PATH_PATTERN.test(path)) {
    return {
      isValid: false,
      error:
        'Invalid path. Use letters, numbers, underscores, dots, hyphens, and spaces. Use / for directories.',
    };
  }
  if (path.split('/').some((segment) => /[ .]$/.test(segment))) {
    return {
      isValid: false,
      error: 'Path segments cannot end with a space or dot.',
    };
  }
  return { isValid: true };
}

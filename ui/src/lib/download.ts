// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { getAuthToken } from './authSession';

/** Saves a blob to disk via a temporary object URL. */
export function downloadBlob(blob: Blob, filename: string): void {
  const link = document.createElement('a');
  const objectUrl = URL.createObjectURL(blob);
  link.href = objectUrl;
  link.download = filename;
  link.click();
  // The URL must outlive the click-initiated navigation; revoking in the
  // same task can abort the download in some browsers.
  window.setTimeout(() => URL.revokeObjectURL(objectUrl), 1000);
}

/**
 * Fetches an authenticated download endpoint and saves the response to disk.
 * The filename comes from the Content-Disposition header when present, else
 * `fallbackFilename`. Throws on a non-OK response.
 */
export async function downloadFromUrl(
  url: string,
  fallbackFilename: string
): Promise<void> {
  const token = getAuthToken();
  const response = await fetch(url, {
    headers: token ? { Authorization: `Bearer ${token}` } : {},
  });

  if (!response.ok) {
    throw new Error(`Download failed: ${response.statusText}`);
  }

  const blob = await response.blob();
  const filename =
    response.headers.get('Content-Disposition')?.match(/filename="(.+)"/)?.[1] ||
    fallbackFilename;
  downloadBlob(blob, filename);
}

/** Requests a native browser download from a same-origin form endpoint. */
export function downloadFromForm(url: string): void {
  const action = new URL(url, window.location.origin);
  if (action.origin !== window.location.origin) {
    throw new Error('Download must use the same origin');
  }
  const form = document.createElement('form');
  form.method = 'post';
  form.action = action.toString();
  form.target = '_blank';
  form.rel = 'noopener';
  form.hidden = true;
  const token = getAuthToken();
  if (token) {
    const input = document.createElement('input');
    input.type = 'hidden';
    input.name = 'token';
    input.value = token;
    form.appendChild(input);
  }
  document.body.appendChild(form);
  try {
    form.submit();
  } finally {
    // Keep the form attached until its navigation task has been scheduled.
    window.setTimeout(() => form.remove(), 0);
  }
}

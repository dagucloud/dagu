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
  URL.revokeObjectURL(objectUrl);
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

// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Query hooks reject with either a `FetchError` carrying the HTTP response or
// the parsed API error body (`{code, message}`). These helpers read both.

export function getQueryErrorStatus(error: unknown): number | undefined {
  const err = error as { status?: number; response?: { status?: number } };
  return err?.status ?? err?.response?.status;
}

export function getQueryErrorCode(error: unknown): string | undefined {
  const code = (error as { code?: unknown })?.code;
  return typeof code === 'string' ? code : undefined;
}

export function getQueryErrorMessage(error: unknown, fallback: string): string {
  const message = (error as { message?: unknown })?.message;
  return typeof message === 'string' && message.length > 0 ? message : fallback;
}

/** Reports whether a query error means the requested resource does not exist. */
export function isNotFoundQueryError(error: unknown): boolean {
  return (
    getQueryErrorStatus(error) === 404 ||
    getQueryErrorCode(error) === 'not_found'
  );
}

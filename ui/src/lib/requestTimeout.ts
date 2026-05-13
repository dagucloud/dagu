// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { handleAuthResponse } from './authSession';

const READ_METHODS = new Set(['GET', 'HEAD']);

export const DEFAULT_READ_TIMEOUT_MS = 10_000;
export const DEFAULT_WRITE_TIMEOUT_MS = 30_000;
export const LIVE_INVALIDATION_COOLDOWN_MS = 5_000;

export class RequestTimeoutError extends Error {
  timeoutMs: number;

  constructor(timeoutMs: number, message: string = 'Request timed out') {
    super(message);
    this.name = 'RequestTimeoutError';
    this.timeoutMs = timeoutMs;
  }
}

export class RequestAbortError extends Error {
  constructor(message: string = 'Request aborted') {
    super(message);
    this.name = 'RequestAbortError';
  }
}

function isRequest(input: RequestInfo | URL): input is Request {
  return typeof Request !== 'undefined' && input instanceof Request;
}

function resolveMethod(input: RequestInfo | URL, init?: RequestInit): string {
  if (init?.method) {
    return init.method.toUpperCase();
  }
  if (isRequest(input)) {
    return input.method.toUpperCase();
  }
  return 'GET';
}

export function getDefaultTimeoutMs(
  input: RequestInfo | URL,
  init?: RequestInit
): number {
  return READ_METHODS.has(resolveMethod(input, init))
    ? DEFAULT_READ_TIMEOUT_MS
    : DEFAULT_WRITE_TIMEOUT_MS;
}

type AbortCleanup = () => void;

function linkAbortSignal(
  source: AbortSignal,
  controller: AbortController
): AbortCleanup {
  const abort = (): void => {
    const reason = source.reason;
    if (reason instanceof RequestTimeoutError) {
      controller.abort(reason);
      return;
    }
    if (reason instanceof RequestAbortError) {
      controller.abort(reason);
      return;
    }
    if (reason instanceof Error) {
      controller.abort(reason);
      return;
    }
    controller.abort(new RequestAbortError());
  };

  if (source.aborted) {
    abort();
    return () => {};
  }

  source.addEventListener('abort', abort, { once: true });
  return () => source.removeEventListener('abort', abort);
}

export async function fetchWithTimeout(
  input: RequestInfo | URL,
  init?: RequestInit
): Promise<Response> {
  const controller = new AbortController();
  const cleanup: AbortCleanup[] = [];
  const timeoutMs = getDefaultTimeoutMs(input, init);
  const signals = new Set<AbortSignal>();

  if (init?.signal) {
    signals.add(init.signal);
  }
  if (isRequest(input) && input.signal) {
    signals.add(input.signal);
  }

  for (const signal of signals) {
    cleanup.push(linkAbortSignal(signal, controller));
    if (controller.signal.aborted) {
      break;
    }
  }

  const timeoutId = globalThis.setTimeout(() => {
    controller.abort(new RequestTimeoutError(timeoutMs));
  }, timeoutMs);

  try {
    const response = await fetch(input, {
      ...init,
      signal: controller.signal,
    });
    handleAuthResponse(response);
    return response;
  } catch (error) {
    if (controller.signal.aborted) {
      const reason = controller.signal.reason;
      if (reason instanceof RequestTimeoutError) {
        throw reason;
      }
      if (reason instanceof RequestAbortError) {
        throw reason;
      }
      if (reason instanceof Error) {
        throw reason;
      }
      throw new RequestAbortError();
    }
    if (isAbortLikeError(error)) {
      throw new RequestAbortError();
    }
    throw error;
  } finally {
    globalThis.clearTimeout(timeoutId);
    cleanup.forEach((fn) => fn());
  }
}

function getErrorResponseStatus(error: unknown): number | undefined {
  if (!error || typeof error !== 'object') {
    return undefined;
  }

  const value = error as {
    response?: { status?: number };
    statusCode?: number;
  };

  return value.response?.status ?? value.statusCode;
}

export function isTimeoutLikeError(error: unknown): boolean {
  if (error instanceof RequestTimeoutError) {
    return true;
  }
  const status = getErrorResponseStatus(error);
  return status === 408 || status === 504;
}

export function isAbortLikeError(error: unknown): boolean {
  if (isTimeoutLikeError(error) || error instanceof RequestAbortError) {
    return true;
  }

  if (!(error instanceof Error)) {
    return false;
  }

  return error.name === 'AbortError';
}

export function shouldRetryQueryError(error: unknown): boolean {
  const status = getErrorResponseStatus(error);
  return !isAbortLikeError(error) && status !== 401;
}

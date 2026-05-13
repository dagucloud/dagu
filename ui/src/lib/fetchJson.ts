declare const getConfig: () => { apiURL: string };

import { getAuthToken } from './authSession';
import { fetchWithTimeout } from './requestTimeout';

/**
 * Fetches JSON from the configured API base URL and returns the parsed response body.
 */
export default async function fetchJson<JSON = unknown>(
  input: RequestInfo,
  init?: RequestInit
): Promise<JSON> {
  const headers: HeadersInit = {
    ...(init?.headers || {}),
    Accept: 'application/json',
  };

  const token = getAuthToken();
  if (token) {
    (headers as Record<string, string>)['Authorization'] = `Bearer ${token}`;
  }

  const response = await fetchWithTimeout(`${getConfig().apiURL}${input}`, {
    ...init,
    headers,
  });
  const data = await response.json().catch(() => undefined);

  if (response.ok) {
    return data;
  }

  throw new FetchError({
    message: data?.message || response.statusText,
    response,
    data: data ?? { message: response.statusText },
  });
}

export class FetchError extends Error {
  response: Response;
  data: {
    message: string;
  };
  constructor({
    message,
    response,
    data,
  }: {
    message: string;
    response: Response;
    data: {
      message: string;
    };
  }) {
    super(message);

    if (Error.captureStackTrace) {
      Error.captureStackTrace(this, FetchError);
    }

    this.name = 'FetchError';
    this.response = response;
    this.data = data ?? { message: message };
  }
}

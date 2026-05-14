import { getAuthToken } from './authSession';

export { getAuthToken };

export function getAuthHeaders(
  additionalHeaders?: Record<string, string>
): Record<string, string> {
  const token = getAuthToken();
  return {
    'Content-Type': 'application/json',
    ...(token ? { Authorization: `Bearer ${token}` } : {}),
    ...additionalHeaders,
  };
}

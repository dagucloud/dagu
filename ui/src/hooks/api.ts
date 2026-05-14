import createClient, { Middleware } from 'openapi-fetch';
import {
  createQueryHook,
  createImmutableHook,
  createInfiniteHook,
} from 'swr-openapi';
import type { paths } from '../api/v1/schema';
import { getAuthToken, handleAuthResponse } from '../lib/authSession';
import { fetchWithTimeout } from '../lib/requestTimeout';

const authMiddleware: Middleware = {
  async onRequest({ request }) {
    const token = getAuthToken();
    if (token) {
      request.headers.set('Authorization', `Bearer ${token}`);
    }
    return request;
  },
  async onResponse({ response }) {
    handleAuthResponse(response);
    return response;
  },
};

const client = createClient<paths>({
  baseUrl: getConfig().apiURL,
  fetch: fetchWithTimeout,
});
client.use(authMiddleware);

const prefix = '/';

// swr-openapi uses `null` init as the real conditional-fetch switch.
// Do not use SWR's `isPaused` to turn GET requests on or off.
export const useQuery = createQueryHook(client, prefix);
export const useImmutable = createImmutableHook(client, prefix);
export const useInfinite = createInfiniteHook(client, prefix);
export const useClient = () => client;

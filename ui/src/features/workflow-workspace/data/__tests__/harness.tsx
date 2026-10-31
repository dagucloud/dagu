// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Shared by the data tests. Each test file mocks '@/hooks/api' with the real
// swr-openapi query hook over a fake client, so SWR keys and dedupe are real.

import { renderHook } from '@testing-library/react';
import type { ReactNode } from 'react';
import {
  MemoryRouter,
  Route,
  Routes,
  useLocation,
  useNavigationType,
} from 'react-router-dom';
import { SWRConfig, type KeyedMutator } from 'swr';
import { useQuery } from '@/hooks/api';
import { WorkspaceDataProvider } from '../WorkspaceDataProvider';
import type { ApiDAGRunDetails, DAGPageData } from '../types';
import { FILE_NAME } from './fixtures';

export interface FakeServer {
  dag: DAGPageData;
  /** Pinned runs by ID. A missing ID answers 404. */
  runs: Record<string, ApiDAGRunDetails>;
  history: { dagRuns: ApiDAGRunDetails[]; gridData: unknown[] };
  subRuns: unknown[];
}

type GetInit = { params?: { path?: Record<string, string> } };

/** Answers the fake client GET from the server state. */
export function serve(server: FakeServer) {
  return async (path: string, init?: GetInit) => {
    switch (path) {
      case '/dags/{fileName}':
        return { data: server.dag };
      case '/dag-runs/{name}/{dagRunId}': {
        const id = init?.params?.path?.dagRunId ?? '';
        const found = server.runs[id];
        return found
          ? { data: { dagRunDetails: found } }
          : {
              error: { code: 'not_found', message: 'not found' },
              response: { status: 404 },
            };
      }
      case '/dags/{fileName}/dag-runs':
        return { data: server.history };
      case '/dag-runs/{name}/{dagRunId}/sub-dag-runs':
        return { data: { subRuns: server.subRuns } };
      default:
        throw new Error(`unexpected GET ${path}`);
    }
  };
}

export interface RenderOptions {
  url?: string;
  refreshDag?: () => Promise<unknown>;
  workspaceName?: string | null;
}

/**
 * Renders the hook inside the page shape: the DAG query feeds
 * WorkspaceDataProvider, like pages/dags/dag/index.tsx.
 */
export function renderWorkspace<T>(
  hook: () => T,
  {
    url = `/dags/${FILE_NAME}`,
    refreshDag,
    workspaceName = null,
  }: RenderOptions = {}
) {
  const page: { mutate: KeyedMutator<DAGPageData> | null } = { mutate: null };

  function Page({ children }: { children: ReactNode }) {
    const { data, mutate } = useQuery('/dags/{fileName}', {
      params: { path: { fileName: FILE_NAME }, query: { remoteNode: 'local' } },
    });
    page.mutate = mutate;
    if (!data) return null;
    return (
      <WorkspaceDataProvider
        fileName={FILE_NAME}
        dagData={data}
        refreshDag={refreshDag ?? (() => mutate())}
        workspaceName={workspaceName}
      >
        {children}
      </WorkspaceDataProvider>
    );
  }

  const swrConfig = {
    provider: () => new Map(),
    shouldRetryOnError: false,
  };
  function Wrapper({ children }: { children: ReactNode }) {
    return (
      <SWRConfig value={swrConfig}>
        <MemoryRouter initialEntries={[url]}>
          {/* The page route gives the hooks their :tab, as in the app. */}
          <Routes>
            <Route
              path="/dags/:fileName/:tab?"
              element={<Page>{children}</Page>}
            />
          </Routes>
        </MemoryRouter>
      </SWRConfig>
    );
  }

  const view = renderHook(
    () => ({
      value: hook(),
      location: useLocation(),
      navigationType: useNavigationType(),
    }),
    { wrapper: Wrapper }
  );
  return { ...view, page };
}

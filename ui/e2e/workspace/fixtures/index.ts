// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  test as base,
  expect,
  type APIRequestContext,
  type Page,
} from '@playwright/test';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import {
  loadStack,
  loginViaAPI,
  loginViaUI,
  uniqueName,
  useDefaultWorkspaceScope,
  waitForDAGAvailable,
  writeLocalDAG,
} from '../../helpers/e2e';

export { expect };

const FIXTURE_DAG_DIR = path.resolve(__dirname, '../../fixtures/dags');

/** Opens an app path. The workflow page is the workspace. */
export async function gotoWorkspace(page: Page, target: string): Promise<void> {
  await page.goto(target);
}

/** Signs in as the stack admin and selects the default workspace scope. */
export async function signIn(page: Page): Promise<void> {
  const stack = await loadStack();
  await loginViaUI(page, stack.auth.adminUsername, stack.auth.adminPassword);
  await useDefaultWorkspaceScope(page);
}

/** Signs in to the API as the stack admin and returns the bearer token. */
export async function adminToken(request: APIRequestContext): Promise<string> {
  const stack = await loadStack();
  return loginViaAPI(
    request,
    stack.auth.adminUsername,
    stack.auth.adminPassword
  );
}

/**
 * Waits until the page is at the path and has these query values. A null
 * value means that the key is absent.
 */
export async function expectAt(
  page: Page,
  pathname: string,
  query: Record<string, string | null>
): Promise<void> {
  await expect
    .poll(
      () => {
        const url = new URL(page.url());
        const values = Object.keys(query).map(
          (key) => `${key}=${url.searchParams.get(key)}`
        );
        return [url.pathname, ...values];
      },
      { timeout: 30_000 }
    )
    .toEqual([
      pathname,
      ...Object.entries(query).map(([key, value]) => `${key}=${value}`),
    ]);
}

export type FixtureDAG = { dagName: string; fileName: string };

type WorkspaceFixtures = {
  gotoWorkspace: (page: Page, target: string) => Promise<void>;
  /**
   * Writes a copy of e2e/fixtures/dags/<file> under a unique name. The copy
   * is deleted after the test.
   */
  fixtureDAG: (file: string, prefix?: string) => Promise<FixtureDAG>;
};

export const test = base.extend<WorkspaceFixtures>({
  // eslint-disable-next-line no-empty-pattern
  gotoWorkspace: async ({}, use) => {
    await use(gotoWorkspace);
  },
  fixtureDAG: async ({ request }, use) => {
    const token = await adminToken(request);
    const created: string[] = [];
    await use(async (file, prefix = 'e2e-ws') => {
      const spec = readFileSync(path.join(FIXTURE_DAG_DIR, file), 'utf8');
      const dagName = uniqueName(prefix);
      const fileName = await writeLocalDAG(dagName, spec);
      created.push(fileName);
      await waitForDAGAvailable(request, token, fileName);
      return { dagName, fileName };
    });
    const leftBehind: string[] = [];
    for (const fileName of created) {
      const response = await request.delete(
        `/api/v1/dags/${encodeURIComponent(fileName)}?remoteNode=local`,
        { headers: { Authorization: `Bearer ${token}` } }
      );
      // 404: the test renamed or deleted the workflow itself.
      if (![204, 404].includes(response.status())) {
        leftBehind.push(`${fileName}: HTTP ${response.status()}`);
      }
    }
    expect(leftBehind, 'fixture DAGs not deleted').toEqual([]);
  },
});

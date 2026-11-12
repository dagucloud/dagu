// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { Request } from '@playwright/test';
import {
  loadStack,
  loginViaAPI,
  selectRemoteNode,
  startDAG,
  uniqueName,
  waitForRunStatus,
  writeRemoteDAG,
} from '../helpers/e2e';
import { expect, signIn, test } from './fixtures';

// A workflow of a remote node in workspace mode: every API request about the
// workflow carries remoteNode=<node> through the one tab (the canvas and the
// Run panel), a run selection, the Workflow panel tabs (with the runtime
// profile) and the launchers
// (follows e2e/remote-nodes.spec.ts).
// parity:shell-remote-node

type SeenRequest = {
  method: string;
  /** Path and query, decoded. */
  url: string;
  path: string;
  remoteNode: string | null;
  /** Decoded URL and body, for the name matches. */
  text: string;
};

function decoded(value: string): string {
  try {
    return decodeURIComponent(value);
  } catch {
    return value;
  }
}

function toSeen(request: Request): SeenRequest | null {
  const url = new URL(request.url());
  if (!url.pathname.startsWith('/api/v1/')) return null;
  const href = decoded(`${url.pathname}${url.search}`);
  let body = '';
  try {
    body = request.postData() ?? '';
  } catch {
    body = '';
  }
  return {
    method: request.method(),
    url: href,
    path: decoded(url.pathname),
    remoteNode: url.searchParams.get('remoteNode'),
    text: `${href}\n${body}`,
  };
}

test.describe('workspace remote node scope', () => {
  // The sidebar node selector, the run list and the Workflow panel need the
  // desktop layout.
  test.skip(
    ({ viewport }) => (viewport?.width ?? 0) < 768,
    'desktop layout only'
  );

  test('keeps remoteNode on every workflow request of a remote DAG', async ({
    page,
    request,
  }) => {
    test.setTimeout(240_000);
    const stack = await loadStack();
    const token = await loginViaAPI(
      request,
      stack.auth.adminUsername,
      stack.auth.adminPassword
    );
    const dagName = uniqueName('e2e-ws-remote');
    // The sleep gives each run its own start second, so the order is fixed.
    const fileName = await writeRemoteDAG(
      dagName,
      `
name: ${dagName}
steps:
  - name: remote-step
    run: |
      sleep 1
      echo remote-output
`
    );
    await expect
      .poll(
        async () => {
          const response = await request.get(
            `${stack.remote.apiBaseURL}/dags/${encodeURIComponent(fileName)}`
          );
          return response.ok();
        },
        { timeout: 15_000 }
      )
      .toBeTruthy();

    await signIn(page);

    const nodeName = uniqueName('ws-remote-node');
    await page.goto('/remote-nodes');
    await page.getByRole('button', { name: 'Add node' }).click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    await dialog.getByLabel('Name').fill(nodeName);
    await dialog.getByLabel('Description').fill('E2E workspace remote node');
    await dialog.getByLabel('API Base URL').fill(stack.remote.apiBaseURL);
    await dialog.getByRole('button', { name: 'Create' }).click();
    await expect(dialog).toBeHidden();
    await expect(
      page.getByRole('row').filter({ hasText: nodeName })
    ).toBeVisible();

    try {
      // Two runs on the remote node, started through the local server.
      const older = await startDAG(request, token, fileName, {}, nodeName);
      await waitForRunStatus(
        request,
        token,
        dagName,
        older,
        ['succeeded'],
        nodeName
      );
      const newer = await startDAG(request, token, fileName, {}, nodeName);
      await waitForRunStatus(
        request,
        token,
        dagName,
        newer,
        ['succeeded'],
        nodeName
      );

      // A page load selects the stored node only when the server listed it
      // at start, so the test stays in this page from here.
      await selectRemoteNode(page, nodeName);

      const seen: SeenRequest[] = [];
      // Requests that do not name the DAG can be global (auth, remote nodes),
      // so they do not fail the test; the report lists those without a node.
      // The runtime profiles of the Overview tab name no DAG but belong to
      // the node.
      const unscoped = new Set<string>();
      page.on('request', (req) => {
        const entry = toSeen(req);
        if (!entry) return;
        if (
          entry.text.includes(dagName) ||
          entry.path.startsWith('/api/v1/profiles')
        ) {
          seen.push(entry);
        } else if (entry.remoteNode !== nodeName) unscoped.add(entry.path);
      });
      /** Waits for a matching request at or after index `from` of seen. */
      const expectRequest = async (
        from: number,
        label: string,
        match: (entry: SeenRequest) => boolean
      ): Promise<void> => {
        await expect
          .poll(() => seen.slice(from).some(match), {
            message: `a ${label} request`,
            timeout: 30_000,
          })
          .toBe(true);
      };

      await test.step('Canvas', async () => {
        await page
          .locator('aside')
          .getByRole('link', { name: 'Workflows' })
          .click();
        await expect(page).toHaveURL(/\/dags$/);
        await page
          .getByRole('main')
          .getByRole('link', { name: dagName, exact: true })
          .first()
          .click();
        await expect(page.getByTestId('workflow-workspace')).toBeVisible({
          timeout: 30_000,
        });
        await expect(
          page.getByTestId('ww-canvas').getByTestId('ve-canvas-node-steps[0]')
        ).toBeVisible({ timeout: 30_000 });
        await expectRequest(0, 'workflow', (entry) =>
          /\/api\/v1\/dags\/[^/]+$/.test(entry.path)
        );
        await expectRequest(0, 'spec', (entry) =>
          /\/api\/v1\/dags\/[^/]+\/spec$/.test(entry.path)
        );
      });

      await test.step('Run panel', async () => {
        // The Run panel starts collapsed.
        const dock = page.getByTestId('ww-dock');
        await expect(dock).toHaveAttribute('data-state', 'collapsed');
        await dock
          .getByRole('button', { name: 'Activity', exact: true })
          .first()
          .click();
        await expect(dock).toHaveAttribute('data-state', 'open');
        const timeline = page.getByTestId('ww-run-timeline');
        await expect(timeline.getByTestId(`ww-run-bar-${older}`)).toBeVisible({
          timeout: 30_000,
        });
        await expect(timeline.getByTestId(`ww-run-bar-${newer}`)).toBeVisible();
        // The Editor may have read the history already.
        await expectRequest(0, 'run history', (entry) =>
          /\/api\/v1\/dags\/[^/]+\/dag-runs$/.test(entry.path)
        );
      });

      await test.step('run selection', async () => {
        const from = seen.length;
        const row = page
          .getByTestId('ww-run-timeline')
          .getByTestId(`ww-run-bar-${older}`);
        await row.click();
        await expect
          .poll(() => new URL(page.url()).searchParams.get('run'))
          .toBe(older);
        await expect(row).toHaveAttribute('aria-selected', 'true');
        await expectRequest(from, 'selected run', (entry) =>
          entry.text.includes(older)
        );
      });

      // The runtime profile is in the Overview tab of the Workflow panel,
      // which opens first; the other sections are its tabs and the launchers
      // of the canvas.
      await test.step('Workflow panel tabs', async () => {
        const profileFrom = seen.length;
        await page
          .getByTestId('ww-pane')
          .getByRole('button', { name: 'Workflow', exact: true })
          .click();
        const tabs = page.getByTestId('ww-workflow-tabs');
        await expect(page.getByTestId('ww-runtime-profile')).toBeVisible({
          timeout: 30_000,
        });
        await expectRequest(profileFrom, 'workflow settings', (entry) =>
          /\/api\/v1\/dags\/[^/]+\/settings$/.test(entry.path)
        );
        // Incidents makes no request without a license, so it is not here.
        const from = seen.length;
        const notifications = tabs.getByRole('tab', {
          name: 'Notifications',
          exact: true,
        });
        await notifications.click();
        await expect(notifications).toHaveAttribute('aria-selected', 'true');
        await expectRequest(from, 'notifications', (entry) =>
          /\/api\/v1\/dags\/[^/]+\/notifications$/.test(entry.path)
        );
      });

      // Webhooks belong to the local node: the canvas draws no Webhook
      // launcher for a remote DAG and sends no webhook request.
      await test.step('Webhook launcher', async () => {
        const canvas = page.getByTestId('ww-canvas');
        await expect(canvas.getByTestId('ve-launcher-manual')).toBeVisible({
          timeout: 30_000,
        });
        await expect(canvas.getByTestId('ve-launcher-cron')).toBeVisible();
        await expect(canvas.getByTestId('ve-launcher-webhook')).toHaveCount(0);
        expect(seen.filter((entry) => /\/webhook/.test(entry.path))).toEqual(
          []
        );
      });

      if (unscoped.size > 0) {
        test.info().annotations.push({
          type: 'requests without remoteNode',
          description: [...unscoped].sort().join(', '),
        });
      }
      const leaks = seen
        .filter((entry) => entry.remoteNode !== nodeName)
        .map((entry) => `${entry.method} ${entry.url}`);
      expect(leaks).toEqual([]);
    } finally {
      await request.delete(
        `/api/v1/dags/${encodeURIComponent(fileName)}?remoteNode=${encodeURIComponent(nodeName)}`,
        { headers: { Authorization: `Bearer ${token}` } }
      );
    }
  });
});

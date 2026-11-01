// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { Locator, Page } from '@playwright/test';
import {
  loadStack,
  loginViaAPI,
  startDAG,
  openStepForm,
  uniqueName,
  waitForDAGAvailable,
  waitForRunStatus,
  writeLocalDAG,
} from '../helpers/e2e';
import { test as base, expect, signIn } from './fixtures';

// parity:test-e2e-other parity:dup-0 parity:test-mermaid-graph - the
// workspace page loads no Mermaid code at runtime; a Wiki page with a Mermaid
// block is the control.

// Present in the Mermaid library and in no module of the app.
const MERMAID_MARKER = 'mermaidAPI';

type InlineDAG = { dagName: string; fileName: string; token: string };

const test = base.extend<{
  /**
   * Writes a DAG with writeLocalDAG. The spec gets the DAG name. The file is
   * deleted after the test.
   */
  inlineDAG: (
    prefix: string,
    spec: (dagName: string) => string
  ) => Promise<InlineDAG>;
}>({
  inlineDAG: async ({ request }, use) => {
    const stack = await loadStack();
    const token = await loginViaAPI(
      request,
      stack.auth.adminUsername,
      stack.auth.adminPassword
    );
    const created: string[] = [];
    await use(async (prefix, spec) => {
      const dagName = uniqueName(prefix);
      const fileName = await writeLocalDAG(dagName, spec(dagName));
      created.push(fileName);
      await waitForDAGAvailable(request, token, fileName);
      return { dagName, fileName, token };
    });
    for (const fileName of created) {
      await request.delete(
        `/api/v1/dags/${encodeURIComponent(fileName)}?remoteNode=local`,
        { headers: { Authorization: `Bearer ${token}` } }
      );
    }
  },
});

type Script = { url: string; body: string };

/**
 * Signs in and leaves the landing page, so the check counts only the
 * documents opened after this.
 */
async function signInAndLeave(page: Page): Promise<void> {
  await signIn(page);
  await page.goto('about:blank');
}

/**
 * Keeps the body of every JavaScript response of the page. Production chunk
 * names are numbers, so a check must read the bodies, not the URLs.
 */
async function collectScripts(page: Page): Promise<() => Promise<Script[]>> {
  // A route turns the HTTP cache of the page off, so every script comes from
  // the network and gives a response event.
  await page.route(
    (url) => url.pathname.endsWith('.js'),
    (route) => route.continue()
  );
  const pending: Promise<Script | null>[] = [];
  page.on('response', (response) => {
    const type = response.headers()['content-type'] ?? '';
    if (!type.includes('javascript')) return;
    pending.push(
      response.text().then(
        (body) => ({ url: response.url(), body }),
        // A body that the browser dropped (redirect, navigation) has no code.
        () => null
      )
    );
  });
  return async () =>
    (await Promise.all(pending)).filter((s): s is Script => s !== null);
}

function withMermaid(scripts: Script[]): string[] {
  return scripts
    .filter((script) => script.body.includes(MERMAID_MARKER))
    .map((script) => script.url);
}

async function openDockTab(page: Page, name: string): Promise<Locator> {
  const dock = page.getByTestId('ww-dock');
  await dock.getByRole('tab', { name, exact: true }).click();
  const panel = dock.getByRole('tabpanel', { name, exact: true });
  await expect(panel).toBeVisible({ timeout: 30_000 });
  return panel;
}

test.describe('workspace without Mermaid', () => {
  // The dock shows every tab inline from 1024px.
  test.skip(
    ({ viewport }) => (viewport?.width ?? 0) < 1024,
    'desktop layout only'
  );

  test('the workspace page loads no Mermaid code', async ({
    page,
    request,
    inlineDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(180_000);
    await signInAndLeave(page);
    const scripts = await collectScripts(page);

    // A markdown artifact without a mermaid fence, and a chat step so that
    // the run has a Chat tab. The chat step has no reachable provider and
    // fails, which ends the run.
    const { dagName, fileName, token } = await inlineDAG(
      'e2e-ws-no-mermaid',
      (name) => `
name: ${name}
artifacts:
  enabled: true
steps:
  - name: report
    action: artifact.write
    with:
      path: report.md
      content: |
        # Workspace report

        A list, and no diagram:

        - one
        - two
  - name: say
    run: echo "no diagram"
  - name: ask
    type: chat
    timeout_sec: 30
    llm:
      provider: openai
      model: e2e-unreachable-model
      base_url: http://127.0.0.1:9/v1
    messages:
      - role: user
        content: hello
`
    );
    const runId = await startDAG(request, token, fileName);
    await waitForRunStatus(
      request,
      token,
      dagName,
      runId,
      ['succeeded', 'failed', 'partially_succeeded'],
      'local',
      90_000
    );

    await gotoWorkspace(page, `/dags/${encodeURIComponent(fileName)}`);
    await expect(page.getByTestId('workflow-workspace')).toBeVisible({
      timeout: 30_000,
    });
    const node = page
      .getByTestId('ww-canvas')
      .getByTestId('ve-canvas-node-steps[0]');
    await expect(node).toBeVisible({ timeout: 30_000 });

    // The one tab shows the latest run: its chip, its flat run icons and the
    // Run panel, which the toggle opens.
    await expect(page.getByTestId('ww-run-chip')).toBeVisible({
      timeout: 30_000,
    });
    const dock = page.getByTestId('ww-dock');
    await dock
      .getByRole('button', { name: 'Activity', exact: true })
      .first()
      .click();
    await expect(dock).toHaveAttribute('data-state', 'open');

    await openDockTab(page, 'Chat');
    const artifacts = await openDockTab(page, 'Artifacts');
    await artifacts
      .getByRole('treeitem', { name: 'report.md', exact: true })
      .click({ timeout: 30_000 });
    await expect(
      artifacts.getByRole('region', { name: 'report.md', exact: true })
    ).toContainText('Workspace report', { timeout: 30_000 });

    // The editor is the same tab. The canvas keeps its view while the panel
    // is open, so the panel closes to leave the step in reach of the
    // pointer; then the step form opens on the canvas.
    await dock
      .getByRole('button', { name: 'Activity', exact: true })
      .first()
      .click();
    await expect(dock).toHaveAttribute('data-state', 'collapsed');
    await node.dblclick();
    await openStepForm(page);
    await expect(page.getByTestId('ve-inspector')).toBeVisible({
      timeout: 30_000,
    });

    // Lazy chunks of the last view may still be on the way.
    await page.waitForTimeout(2_000);
    const loaded = await scripts();
    // The app bundle and at least one lazy chunk.
    expect(loaded.length).toBeGreaterThan(1);
    expect(withMermaid(loaded)).toEqual([]);
  });

  // A Wiki page with a Mermaid block loads Mermaid, so it shows that the
  // check sees it.
  test('a Wiki page with a Mermaid block loads Mermaid (control)', async ({
    page,
    request,
  }) => {
    test.setTimeout(120_000);
    const stack = await loadStack();
    const token = await loginViaAPI(
      request,
      stack.auth.adminUsername,
      stack.auth.adminPassword
    );
    const headers = { Authorization: `Bearer ${token}` };
    const id = uniqueName('e2e-ws-mmd-wiki');
    const created = await request.post('/api/v1/wiki?remoteNode=local', {
      headers,
      data: {
        id,
        content: '# Control\n\n```mermaid\ngraph TD\n  A --> B\n```\n',
      },
    });
    expect(created.ok()).toBe(true);
    try {
      await signInAndLeave(page);
      const scripts = await collectScripts(page);

      await page.goto(`/wiki/${id}`);
      // The page opens in the editor; the preview draws the block.
      await page.getByRole('button', { name: 'Preview', exact: true }).click();
      await expect
        .poll(async () => withMermaid(await scripts()).length, {
          timeout: 30_000,
        })
        .toBeGreaterThan(0);
    } finally {
      await request.delete(
        `/api/v1/wiki/page?path=${encodeURIComponent(id)}&remoteNode=local`,
        { headers }
      );
    }
  });
});

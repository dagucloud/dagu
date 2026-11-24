// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  startDAG,
  uniqueName,
  waitForDAGAvailable,
  waitForRunStatus,
  writeLocalDAG,
} from '../helpers/e2e';
import { adminToken, expect, signIn, test } from './fixtures';

// A run whose steps fan out over inventory nodes (RFC 013 §Run view): the
// Nodes tab lists each node, failed first, the step cards count the nodes,
// and a node's child run names its node. The children only run noop and
// js.run steps, so no node is reached over SSH.
test.describe('Node runs', () => {
  const created: string[] = [];

  test.afterEach(async ({ request }) => {
    const token = await adminToken(request);
    for (const fileName of created.splice(0)) {
      await request.delete(
        `/api/v1/dags/${encodeURIComponent(fileName)}?remoteNode=local`,
        { headers: { Authorization: `Bearer ${token}` } }
      );
    }
  });

  test('lists the nodes of a run, failed first, and opens a node run', async ({
    page,
    request,
    gotoWorkspace,
  }) => {
    test.setTimeout(120_000);
    const token = await adminToken(request);
    // Other specs share the server: the label and the node names are unique.
    const id = uniqueName('e2e-nodes');
    const [web01, web02, web03] = ['web01', 'web02', 'web03'].map(
      (name) => `${id}-${name}`
    );
    const imported = await request.post('/api/v1/inventory-imports', {
      headers: { Authorization: `Bearer ${token}` },
      data: [
        {
          name: `${id} fleet`,
          nodes: [
            { name: web01, host: '10.0.1.11', labels: [id] },
            { name: web02, host: '10.0.1.12', labels: [id], disabled: true },
            { name: web03, host: '10.0.1.13', labels: [id] },
          ],
        },
      ],
    });
    expect(imported.ok(), await imported.text()).toBeTruthy();

    const dagName = uniqueName('e2e-node-runs');
    const fileName = await writeLocalDAG(
      dagName,
      `
steps:
  - id: deploy
    action: dag.run
    with:
      dag: deploy-one
    nodes: ${id}
  - id: check
    depends: [deploy]
    action: dag.run
    with:
      dag: check-one
    nodes:
      labels: ${id}
      keep_going: true
---
name: deploy-one
node: required
steps:
  - action: noop
---
name: check-one
node: required
steps:
  - id: ping
    action: noop
  - id: probe
    depends: [ping]
    action: js.run
    preconditions:
      - condition: "\${node.name}"
        expected: ${web03}
    with:
      script: throw new Error("down")
`
    );
    created.push(fileName);
    await waitForDAGAvailable(request, token, fileName);
    const runId = await startDAG(request, token, fileName);
    await waitForRunStatus(request, token, dagName, runId, ['failed']);

    await signIn(page);
    const runPath = `/dags/${encodeURIComponent(fileName)}?run=${encodeURIComponent(runId)}`;
    // The step card of check counts its nodes. A phone shows the graph or the
    // dock, not both, so the card is read before the dock opens.
    await gotoWorkspace(page, runPath);
    await expect(
      page.getByTestId('ww-canvas').getByRole('img', {
        name: '1 succeeded, 1 failed, 0 running of 3 nodes',
      })
    ).toBeVisible({ timeout: 30_000 });

    await gotoWorkspace(page, `${runPath}&dock=nodes`);
    await expect(
      page
        .getByTestId('ww-dock')
        .getByRole('tab', { name: 'Nodes', exact: true })
    ).toHaveAttribute('aria-selected', 'true', { timeout: 30_000 });

    const rows = page.getByTestId('node-runs-row');
    await expect(rows.getByRole('rowheader')).toHaveText([web03, web01, web02]);

    await page.getByRole('button', { name: 'Failed only' }).click();
    await expect(rows.getByRole('rowheader')).toHaveText([web03]);

    await page
      .getByRole('button', { name: `Open the run of ${web03} for check` })
      .click();
    await expect(page.getByRole('dialog').getByTitle('Node')).toContainText(
      web03
    );
  });
});

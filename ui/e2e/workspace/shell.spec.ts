// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { APIRequestContext, Locator, Page } from '@playwright/test';
import {
  clearSession,
  createUser,
  hasRBACLicenseSourceConfigured,
  loadStack,
  loginViaAPI,
  loginViaUI,
  openStepForm,
  uniqueName,
  useDefaultWorkspaceScope,
} from '../helpers/e2e';
import { expect, signIn, test } from './fixtures';

// parity:test-e2e-dag-crud (workspace twin of e2e/dag-crud.spec.ts: edit and
// save, external change, reader, Start, rename, delete and pin)

function apiHeaders(token: string): Record<string, string> {
  return {
    Authorization: `Bearer ${token}`,
    'Content-Type': 'application/json',
  };
}

function dagURL(fileName: string, suffix = ''): string {
  return `/api/v1/dags/${encodeURIComponent(fileName)}${suffix}?remoteNode=local`;
}

async function adminToken(request: APIRequestContext): Promise<string> {
  const stack = await loadStack();
  return loginViaAPI(
    request,
    stack.auth.adminUsername,
    stack.auth.adminPassword
  );
}

async function readSpec(
  request: APIRequestContext,
  token: string,
  fileName: string
): Promise<string> {
  const response = await request.get(dagURL(fileName, '/spec'), {
    headers: apiHeaders(token),
  });
  expect(response.ok()).toBeTruthy();
  return ((await response.json()) as { spec: string }).spec;
}

// Writes without expectedSha256, like another writer on the server.
async function writeSpec(
  request: APIRequestContext,
  token: string,
  fileName: string,
  spec: string
): Promise<void> {
  const response = await request.put(dagURL(fileName, '/spec'), {
    headers: apiHeaders(token),
    data: { spec },
  });
  expect(response.ok(), await response.text()).toBeTruthy();
}

function canvasNode(page: Page, key: string): Locator {
  return page.getByTestId('ww-canvas').getByTestId(`ve-canvas-node-${key}`);
}

function saveButton(page: Page): Locator {
  return page.locator('#save-config');
}

/** Opens the workspace on the DAG and waits for the canvas to show steps. */
async function openWorkspace(
  page: Page,
  gotoWorkspace: (page: Page, target: string) => Promise<void>,
  fileName: string
): Promise<void> {
  await gotoWorkspace(page, `/dags/${encodeURIComponent(fileName)}`);
  await expect(page.getByTestId('workflow-workspace')).toBeVisible({
    timeout: 30_000,
  });
  await expect(canvasNode(page, 'steps[0]')).toBeVisible({ timeout: 30_000 });
}

/** Opens the NDV of the first step and sets its description. */
async function editFirstStepDescription(
  page: Page,
  text: string
): Promise<void> {
  await canvasNode(page, 'steps[0]').dblclick();
  await openStepForm(page);
  const inspector = page.getByTestId('ve-inspector');
  await expect(inspector).toBeVisible({ timeout: 30_000 });
  const description = inspector.getByLabel('Description', { exact: true });
  await description.fill(text);
  // The field sends its draft on blur. A blur, not Tab, leaves the focus on
  // the page, so the next key goes to the document and not to a field.
  await description.blur();
  await expect(saveButton(page)).toBeEnabled({ timeout: 30_000 });
}

/** A button of the workflow bar: Rename and Delete are icon buttons of it. */
function barButton(page: Page, name: string): Locator {
  return page.getByTestId('ww-bar').getByRole('button', { name, exact: true });
}

test.describe('workspace shell', () => {
  // The pin and the canvas NDV need the desktop layout.
  test.skip(
    ({ viewport }) => (viewport?.width ?? 0) < 768,
    'desktop layout only'
  );

  test.beforeEach(async ({ page }) => {
    await signIn(page);
  });

  // parity:spec-save-diff-dialog parity:spec-save-request
  test('saves a step field with Ctrl+S through the diff dialog', async ({
    page,
    request,
    fixtureDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(120_000);
    const token = await adminToken(request);
    const { fileName } = await fixtureDAG('visual-params.yaml');
    const description = 'Saved from the workspace canvas';

    await openWorkspace(page, gotoWorkspace, fileName);
    await expect(saveButton(page)).toBeDisabled();
    await editFirstStepDescription(page, description);

    await page.keyboard.press('ControlOrMeta+s');
    const review = page.getByRole('dialog', { name: 'Review changes' });
    await expect(review).toBeVisible({ timeout: 30_000 });
    await expect(review).toContainText(description);

    const saved = page.waitForResponse(
      (response) =>
        response.request().method() === 'PUT' &&
        new URL(response.url()).pathname.endsWith('/spec')
    );
    await review.getByRole('button', { name: 'Save', exact: true }).click();
    expect((await saved).ok()).toBeTruthy();
    await expect(review).toBeHidden({ timeout: 30_000 });
    await expect(saveButton(page)).toBeDisabled({ timeout: 30_000 });

    expect(await readSpec(request, token, fileName)).toContain(
      `description: ${description}`
    );
  });

  // The Save diff colours the changed lines in both views: the background of
  // an added line is not transparent, the sign cell has a bar, and an
  // unchanged line keeps the plain background.
  test('colours the added lines of the Save diff in both views', async ({
    page,
    fixtureDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(120_000);
    const { fileName } = await fixtureDAG('visual-params.yaml');

    await openWorkspace(page, gotoWorkspace, fileName);
    await editFirstStepDescription(page, 'A line that the diff colours');
    await page.keyboard.press('ControlOrMeta+s');
    const review = page.getByRole('dialog', { name: 'Review changes' });
    await expect(review).toBeVisible({ timeout: 30_000 });

    const background = (cell: Locator) =>
      cell.evaluate((td) => getComputedStyle(td).backgroundColor);
    for (const mode of ['Split', 'Unified']) {
      await review.getByRole('button', { name: mode, exact: true }).click();
      const added = review.locator('td[class*="-diff-added"]');
      await expect(added.first()).toBeVisible();
      const colours = await added.evaluateAll((cells) =>
        cells.map((td) => getComputedStyle(td).backgroundColor)
      );
      expect(colours.length).toBeGreaterThan(0);
      for (const colour of colours) {
        expect(colour, `${mode}: added cell`).not.toBe('rgba(0, 0, 0, 0)');
      }
      const bar = review
        .locator('td[class*="-marker"][class*="-diff-added"]')
        .first();
      expect(
        await bar.evaluate((td) => getComputedStyle(td).boxShadow),
        `${mode}: bar of an added line`
      ).not.toBe('none');
      const plain = review
        .locator(
          'td[class*="-content"]:not([class*="-diff-added"]):not([class*="-diff-removed"])'
        )
        .filter({ hasText: 'name: prepare' })
        .first();
      const plainColour = await background(plain);
      expect(plainColour, `${mode}: unchanged line`).not.toBe(colours[0]);
    }
  });

  // parity:spec-external-change-dialog
  test('shows the external change dialog after a server-side write', async ({
    page,
    request,
    fixtureDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(150_000);
    const token = await adminToken(request);
    const { fileName } = await fixtureDAG('visual-params.yaml');
    const original = await readSpec(request, token, fileName);

    await openWorkspace(page, gotoWorkspace, fileName);
    await editFirstStepDescription(page, 'Edited in the browser');

    const external = `${original}# changed on disk by another writer\n`;
    await writeSpec(request, token, fileName, external);

    // The live spec stream shows the change; a save attempt that the server
    // rejects (409) loads it too. Either way the dialog must come.
    const externalDialog = page.getByRole('dialog', {
      name: 'External Changes Detected',
    });
    const review = page.getByRole('dialog', { name: 'Review changes' });
    await expect(async () => {
      if (!(await externalDialog.isVisible())) {
        if (await review.isVisible()) {
          await review
            .getByRole('button', { name: 'Save', exact: true })
            .click({ timeout: 3_000 });
        } else {
          await page.keyboard.press('ControlOrMeta+s');
        }
      }
      await expect(externalDialog).toBeVisible({ timeout: 10_000 });
    }).toPass({ timeout: 90_000, intervals: [2_000, 5_000] });

    // A rejected save opens its error before the newer text arrives, so the
    // external change dialog is the top layer: Escape would ignore it. Answer
    // it first, then close what stays below it.
    await externalDialog
      .getByRole('button', { name: 'Discard & Reload' })
      .click();
    await expect(externalDialog).toBeHidden();
    const rejected = page
      .getByRole('dialog')
      .filter({ hasText: 'The file changed on disk' });
    if (await rejected.isVisible()) {
      // The corner button and the footer button both read 'Close'.
      await rejected
        .getByRole('button', { name: 'Close', exact: true })
        .first()
        .click();
      await expect(rejected).toBeHidden();
    }
    if (await review.isVisible()) {
      await review.getByRole('button', { name: 'Cancel', exact: true }).click();
      await expect(review).toBeHidden();
    }
    await expect(saveButton(page)).toBeDisabled({ timeout: 30_000 });
    // No silent overwrite: the server keeps the external text.
    expect(await readSpec(request, token, fileName)).toBe(external);
  });

  // parity:canvas-readonly-chip parity:edit-permission
  test('shows a reader the read-only chip and no edit controls', async ({
    page,
    request,
    fixtureDAG,
    gotoWorkspace,
  }) => {
    test.skip(
      !hasRBACLicenseSourceConfigured(),
      'requires a Dagu Pro license source for RBAC user management'
    );
    const token = await adminToken(request);
    const { dagName, fileName } = await fixtureDAG('visual-params.yaml');
    const username = uniqueName('ws-viewer');
    const password = 'viewer-pass-123';
    await createUser(request, token, { username, password, role: 'viewer' });

    await clearSession(page);
    await loginViaUI(page, username, password);
    await useDefaultWorkspaceScope(page);
    await openWorkspace(page, gotoWorkspace, fileName);

    const canvas = page.getByTestId('ww-canvas');
    await expect(canvas.getByText('Read-only', { exact: true })).toBeVisible();
    // A reader has no button to add a step.
    await expect(
      canvas.getByRole('button', { name: 'Add a step' })
    ).toHaveCount(0);
    await expect(saveButton(page)).toHaveCount(0);
    await expect(
      page.getByRole('button', { name: `Pin workflow ${dagName}` })
    ).toHaveCount(0);

    await expect(barButton(page, 'Rename')).toHaveCount(0);
    await expect(barButton(page, 'Delete')).toHaveCount(0);
  });

  // parity:run-dag-button parity:canvas-run-overlay
  test('Start changes the status of the nodes', async ({
    page,
    fixtureDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(150_000);
    const { fileName } = await fixtureDAG('visual-params.yaml');

    await openWorkspace(page, gotoWorkspace, fileName);
    // The workflow has no run yet: no run status on its nodes.
    await expect(
      canvasNode(page, 'steps[0]').getByTestId('ve-canvas-node-status')
    ).toHaveCount(0);

    await page
      .getByTestId('ww-run-split')
      .locator('[data-ww-action="start"]')
      .click();
    const startDialog = page.getByRole('dialog', { name: 'Start workflow' });
    await startDialog
      .getByRole('button', { name: 'Start', exact: true })
      .click();

    // Start selects the new run on the same page: ?run names it, and its
    // statuses overlay the editable graph.
    await expect(page).toHaveURL(
      new RegExp(`/dags/${encodeURIComponent(fileName)}\\?.*run=`),
      { timeout: 30_000 }
    );
    const runCanvas = page.getByTestId('ww-canvas');
    const first = runCanvas
      .getByTestId('ve-canvas-node-steps[0]')
      .getByTestId('ve-canvas-node-status');
    const last = runCanvas
      .getByTestId('ve-canvas-node-steps[2]')
      .getByTestId('ve-canvas-node-status');

    // NodeStatus.Success in ui/src/api/v1/schema.ts.
    await expect(first).toHaveAttribute('data-status', '4', {
      timeout: 90_000,
    });
    await expect(last).toHaveAttribute('data-status', '4', {
      timeout: 90_000,
    });
  });

  // parity:edit-rename
  test('renames the workflow from the bar', async ({
    page,
    request,
    fixtureDAG,
    gotoWorkspace,
  }) => {
    const token = await adminToken(request);
    const { dagName, fileName } = await fixtureDAG('visual-params.yaml');
    const newName = uniqueName('e2e-ws-renamed');

    try {
      await openWorkspace(page, gotoWorkspace, fileName);
      await expect(
        page
          .getByTestId('ww-bar')
          .getByRole('heading', { level: 1, name: dagName, exact: true })
      ).toBeVisible();

      await barButton(page, 'Rename').click();
      const dialog = page.getByRole('dialog');
      await expect(dialog).toBeVisible();
      await dialog.getByLabel('Workflow name').clear();
      await dialog.getByLabel('Workflow name').fill(newName);
      await dialog.getByRole('button', { name: 'Rename' }).click();

      await expect
        .poll(() => {
          const url = new URL(page.url());
          return `${url.pathname}?remoteNode=${url.searchParams.get('remoteNode')}`;
        })
        .toBe(`/dags/${encodeURIComponent(newName)}?remoteNode=local`);
      const renamed = await request.get(dagURL(`${newName}.yaml`), {
        headers: apiHeaders(token),
      });
      expect(renamed.ok()).toBeTruthy();
    } finally {
      await request.delete(dagURL(`${newName}.yaml`), {
        headers: apiHeaders(token),
      });
    }
  });

  // parity:edit-delete
  test('deletes the workflow from the bar', async ({
    page,
    request,
    fixtureDAG,
    gotoWorkspace,
  }) => {
    const token = await adminToken(request);
    const { fileName } = await fixtureDAG('visual-params.yaml');

    await openWorkspace(page, gotoWorkspace, fileName);
    await barButton(page, 'Delete').click();

    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    await expect(dialog).toContainText('Delete workflow');
    await expect(dialog).toContainText(fileName);
    await dialog.getByRole('button', { name: 'Delete', exact: true }).click();

    await expect(page).toHaveURL(/\/dags\/?\?remoteNode=local$/);
    const response = await request.get(dagURL(fileName), {
      headers: apiHeaders(token),
    });
    expect(response.ok()).toBeFalsy();
  });

  // Pins are shared by every user of the server, so the test always unpins.
  // parity:header-pin
  test('pins and unpins the workflow from the bar', async ({
    page,
    request,
    fixtureDAG,
    gotoWorkspace,
  }) => {
    const token = await adminToken(request);
    const { dagName, fileName } = await fixtureDAG('visual-params.yaml');
    const isPinned = async () => {
      const response = await request.get(dagURL(fileName), {
        headers: apiHeaders(token),
      });
      return ((await response.json()) as { pinned: boolean }).pinned;
    };

    try {
      await openWorkspace(page, gotoWorkspace, fileName);
      const pin = page
        .getByTestId('ww-bar')
        .getByRole('button', { name: `Pin workflow ${dagName}` });
      await expect(pin).toHaveAttribute('aria-pressed', 'false');

      await pin.click();
      await expect.poll(isPinned, { timeout: 15_000 }).toBe(true);
      await expect(pin).toHaveAttribute('aria-pressed', 'true');

      await pin.click();
      await expect.poll(isPinned, { timeout: 15_000 }).toBe(false);
      await expect(pin).toHaveAttribute('aria-pressed', 'false');
    } finally {
      await request.delete(dagURL(fileName, '/pin'), {
        headers: apiHeaders(token),
      });
    }
  });
});

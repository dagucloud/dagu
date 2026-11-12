// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  expect,
  test,
  type APIRequestContext,
  type Locator,
  type Page,
} from '@playwright/test';
import { readdirSync, readFileSync } from 'node:fs';
import path from 'node:path';
import {
  loadStack,
  loginViaAPI,
  loginViaUI,
  openStepForm,
  uniqueName,
  useDefaultWorkspaceScope,
  waitForDAGAvailable,
  writeLocalDAG,
} from './helpers/e2e';

const FIXTURE_DIR = path.resolve(__dirname, 'fixtures/dags');
const fixtureFiles = readdirSync(FIXTURE_DIR)
  .filter((file) => file.endsWith('.yaml'))
  .sort();

function fixtureText(file: string): string {
  return readFileSync(path.join(FIXTURE_DIR, file), 'utf8');
}

function apiHeaders(token: string): Record<string, string> {
  return {
    Authorization: `Bearer ${token}`,
    'Content-Type': 'application/json',
  };
}

function specURL(fileName: string): string {
  return `/api/v1/dags/${encodeURIComponent(fileName)}/spec?remoteNode=local`;
}

async function readSpec(
  request: APIRequestContext,
  token: string,
  fileName: string
): Promise<string> {
  const response = await request.get(specURL(fileName), {
    headers: apiHeaders(token),
  });
  expect(response.ok()).toBeTruthy();
  return ((await response.json()) as { spec: string }).spec;
}

// Writes without expectedSha256, like an editor on disk would.
async function writeSpec(
  request: APIRequestContext,
  token: string,
  fileName: string,
  spec: string
): Promise<void> {
  const response = await request.put(specURL(fileName), {
    headers: apiHeaders(token),
    data: { spec },
  });
  const failureBody = response.ok() ? '' : await response.text();
  expect(
    response.ok(),
    `PUT spec failed with ${response.status()}: ${failureBody}`
  ).toBeTruthy();
}

function canvasNode(page: Page, key: string): Locator {
  return page.getByTestId(`ve-canvas-node-${key}`);
}

function saveButton(page: Page): Locator {
  return page.locator('#save-config');
}

// Opens the workflow page, whose editor is the visual editor, and waits for
// the canvas to show steps.
async function openVisual(page: Page, fileName: string): Promise<void> {
  await page.goto(`/dags/${encodeURIComponent(fileName)}`);
  await expect(page.getByTestId('ve-canvas')).toBeVisible({ timeout: 30_000 });
  await expect(canvasNode(page, 'steps[0]')).toBeVisible({ timeout: 30_000 });
}

// Adds a step after the selected step. The new step becomes the selection.
async function addStepFromPalette(page: Page, action: string): Promise<void> {
  // The n shortcut of the focused canvas opens the list of steps in the
  // side drawer.
  await page.getByTestId('ve-canvas').press('n');
  const panel = page.getByTestId('ve-add-step');
  const search = panel.getByRole('combobox', { name: 'Search actions' });
  await expect(search).toBeVisible({ timeout: 30_000 });
  await search.fill(action);
  await expect(panel.getByRole('option').first()).toBeVisible({
    timeout: 30_000,
  });
  await search.press('Enter');
  await expect(search).toBeHidden({ timeout: 30_000 });
}

// parity:test-e2e-visual-editor
test.describe('visual editor', () => {
  const createdFiles: string[] = [];

  async function createDAG(
    request: APIRequestContext,
    token: string,
    prefix: string,
    spec: string
  ): Promise<{ dagName: string; fileName: string; written: string }> {
    const dagName = uniqueName(prefix);
    const fileName = await writeLocalDAG(dagName, spec);
    createdFiles.push(fileName);
    await waitForDAGAvailable(request, token, fileName);
    return { dagName, fileName, written: `${spec.trim()}\n` };
  }

  test.beforeEach(async ({ page }) => {
    const stack = await loadStack();
    await loginViaUI(page, stack.auth.adminUsername, stack.auth.adminPassword);
    await useDefaultWorkspaceScope(page);
    await page.setViewportSize({ width: 1600, height: 900 });
  });

  test.afterEach(async ({ request }) => {
    const stack = await loadStack();
    const token = await loginViaAPI(
      request,
      stack.auth.adminUsername,
      stack.auth.adminPassword
    );
    const files = createdFiles.splice(0);
    for (const fileName of files) {
      const response = await request.delete(
        `/api/v1/dags/${encodeURIComponent(fileName)}?remoteNode=local`,
        { headers: apiHeaders(token) }
      );
      expect(response.status(), `delete ${fileName}`).toBe(204);
    }
  });

  for (const file of fixtureFiles) {
    // Opening a DAG in the Visual view and selecting a step must not change
    // the file, and nothing may enable Save.
    test(`golden: ${file} stays byte-identical`, async ({ page, request }) => {
      test.setTimeout(120_000);
      const stack = await loadStack();
      const token = await loginViaAPI(
        request,
        stack.auth.adminUsername,
        stack.auth.adminPassword
      );
      const { fileName, written } = await createDAG(
        request,
        token,
        'e2e-ve-golden',
        fixtureText(file)
      );

      await openVisual(page, fileName);
      await expect(saveButton(page)).toBeDisabled();

      // The inspector must not write to the buffer when it opens.
      await canvasNode(page, 'steps[0]').dblclick();
      await openStepForm(page);
      await expect(page.getByTestId('ve-inspector')).toBeVisible({
        timeout: 30_000,
      });
      // Give validation and form mount effects time to run.
      await page.waitForTimeout(2_000);
      await expect(saveButton(page)).toBeDisabled();

      expect(await readSpec(request, token, fileName)).toBe(written);
    });
  }

  test('adds a step and saves it without touching the other lines', async ({
    page,
    request,
  }) => {
    test.setTimeout(180_000);
    const stack = await loadStack();
    const token = await loginViaAPI(
      request,
      stack.auth.adminUsername,
      stack.auth.adminPassword
    );
    const { fileName, written } = await createDAG(
      request,
      token,
      'e2e-ve-add',
      fixtureText('visual-params.yaml')
    );

    await openVisual(page, fileName);

    // The node view of a step covers the canvas toolbar, so the step is
    // added after a selection and its view opens afterwards.
    await canvasNode(page, 'steps[0]').click();
    await addStepFromPalette(page, 'http.request');
    await expect(canvasNode(page, 'steps[1]')).toBeVisible({ timeout: 30_000 });
    await canvasNode(page, 'steps[1]').dblclick();
    await openStepForm(page);
    const inspector = page.getByTestId('ve-inspector');
    await expect(inspector).toBeVisible({ timeout: 30_000 });

    const url = inspector.getByLabel(/^url$/i);
    await expect(url).toBeVisible({ timeout: 30_000 });
    await url.fill('https://example.com/');
    await expect(url).toHaveValue('https://example.com/');

    // The candidates come from the validation of the edited text, so the
    // picker is opened again until the parameter is listed.
    const picker = page.getByRole('listbox', { name: 'References' });
    const param = picker
      .getByRole('option')
      .filter({ hasText: 'params.GREETING' })
      .first();
    await expect(async () => {
      if (await picker.isVisible()) {
        await page.keyboard.press('Escape');
      }
      await url
        .locator('..')
        .getByRole('button', { name: 'Insert reference' })
        .click({ timeout: 5_000 });
      await expect(param).toBeVisible({ timeout: 3_000 });
    }).toPass({ timeout: 60_000 });
    await param.click();
    await expect(url).toHaveValue('https://example.com/${params.GREETING}');

    await expect(saveButton(page)).toBeEnabled();
    // The node view covers the Save button, so the shortcut saves.
    await page.keyboard.press('ControlOrMeta+s');
    const review = page.getByRole('dialog', { name: 'Review changes' });
    await expect(review).toBeVisible({ timeout: 30_000 });
    await expect(review).toContainText('example.com');

    const saved = page.waitForResponse(
      (response) =>
        response.request().method() === 'PUT' &&
        new URL(response.url()).pathname.endsWith('/spec')
    );
    await review.getByRole('button', { name: 'Save', exact: true }).click();
    expect((await saved).ok()).toBeTruthy();
    await expect(review).toBeHidden({ timeout: 30_000 });
    await expect(saveButton(page)).toBeDisabled({ timeout: 30_000 });

    const spec = await readSpec(request, token, fileName);
    // The save inserts one block: the lines before it and after it are the
    // lines of the file.
    const before = written.split('\n');
    const after = spec.split('\n');
    let head = 0;
    while (head < before.length && before[head] === after[head]) head++;
    let tail = 0;
    while (
      tail < before.length - head &&
      before[before.length - 1 - tail] === after[after.length - 1 - tail]
    )
      tail++;
    expect(head + tail).toBe(before.length);
    expect(spec).toContain('action: http.request');
    expect(spec).toMatch(
      /url: ["']?https:\/\/example\.com\/\$\{params\.GREETING\}["']?/
    );

    // The node view of the new step is still open on its form. The YAML
    // button of its mode switch shows the text of the step.
    const nodeView = page.getByTestId('ndv');
    await nodeView.getByRole('button', { name: 'YAML', exact: true }).click();
    const editor = nodeView.locator('.monaco-editor').first();
    await expect(editor).toBeVisible({ timeout: 30_000 });
    await expect(editor.locator('.view-lines')).toContainText(
      /example\.com\/\$\{params\.GREETING\}/,
      { timeout: 30_000 }
    );
  });

  // Run step from the canvas: e2e/workspace/run-actions.spec.ts. The editor of
  // the workspace shows the definition only, with no run status on its nodes.

  // An edited page learns of a change on disk from the live feed, and its
  // edits wait for a decision.
  test('shows a change on disk while the edits are unsaved', async ({
    page,
    request,
  }) => {
    test.setTimeout(180_000);
    const stack = await loadStack();
    const token = await loginViaAPI(
      request,
      stack.auth.adminUsername,
      stack.auth.adminPassword
    );
    const { fileName, written } = await createDAG(
      request,
      token,
      'e2e-ve-live',
      fixtureText('visual-params.yaml')
    );

    await openVisual(page, fileName);
    await canvasNode(page, 'steps[0]').click();
    await addStepFromPalette(page, 'http.request');
    await expect(canvasNode(page, 'steps[1]')).toBeVisible({ timeout: 30_000 });
    await expect(saveButton(page)).toBeEnabled({ timeout: 30_000 });

    // Another writer changes the file after the page loaded it.
    const external = `${written}# changed on disk by another writer\n`;
    await writeSpec(request, token, fileName, external);

    await expect(
      page.getByRole('dialog', { name: 'External Changes Detected' })
    ).toBeVisible({ timeout: 30_000 });
    expect(await readSpec(request, token, fileName)).toBe(external);
  });

  // A save that reaches the server after a change on disk is refused.
  test('does not overwrite a file that changed on disk', async ({
    page,
    request,
  }) => {
    test.setTimeout(180_000);
    const stack = await loadStack();
    const token = await loginViaAPI(
      request,
      stack.auth.adminUsername,
      stack.auth.adminPassword
    );
    const { fileName, written } = await createDAG(
      request,
      token,
      'e2e-ve-conflict',
      fixtureText('visual-params.yaml')
    );

    await openVisual(page, fileName);
    await canvasNode(page, 'steps[0]').click();
    await addStepFromPalette(page, 'http.request');
    await expect(canvasNode(page, 'steps[1]')).toBeVisible({ timeout: 30_000 });
    await expect(saveButton(page)).toBeEnabled({ timeout: 30_000 });

    // Another writer changes the file while the save is on its way, so the
    // page cannot learn of the change before the server checks the save.
    const external = `${written}# changed on disk by another writer\n`;
    await page.route(
      (url) => url.pathname.endsWith('/spec'),
      async (route) => {
        if (route.request().method() !== 'PUT') {
          await route.fallback();
          return;
        }
        await writeSpec(request, token, fileName, external);
        await route.continue();
      }
    );

    const saved = page.waitForResponse(
      (response) =>
        response.request().method() === 'PUT' &&
        new URL(response.url()).pathname.endsWith('/spec')
    );
    await saveButton(page).click();
    const review = page.getByRole('dialog', { name: 'Review changes' });
    await expect(review).toBeVisible({ timeout: 30_000 });
    await review.getByRole('button', { name: 'Save', exact: true }).click();
    expect((await saved).status()).toBe(409);
    await expect(page.getByText('The file changed on disk')).toBeVisible({
      timeout: 30_000,
    });

    // No silent overwrite: the server keeps the external text.
    expect(await readSpec(request, token, fileName)).toBe(external);
  });
});

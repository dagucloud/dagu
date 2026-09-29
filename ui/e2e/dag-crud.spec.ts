// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { expect, test, type Page } from '@playwright/test';
import {
  loadStack,
  loginViaAPI,
  loginViaUI,
  uniqueName,
  useDefaultWorkspaceScope,
  waitForDAGAvailable,
  writeLocalDAG,
} from './helpers/e2e';

function dagDefinitionsEntry(page: Page, dagName: string) {
  return page
    .locator('.card-obsidian:visible, tr:visible')
    .filter({ hasText: dagName })
    .first();
}

function localScopedURL(baseURL: string, path: string) {
  const url = new URL(path, baseURL);
  url.searchParams.set('remoteNode', 'local');
  return url.toString();
}

test.describe('DAG CRUD operations', () => {
  test.beforeEach(async ({ page }) => {
    const stack = await loadStack();
    await loginViaUI(page, stack.auth.adminUsername, stack.auth.adminPassword);
    await useDefaultWorkspaceScope(page);
  });

  test('creates a new DAG from the UI', async ({ page }) => {
    const dagName = uniqueName('e2e-create');

    await page.goto('/dags/');
    await page.getByRole('button', { name: 'Create new DAG' }).click();

    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    await dialog.getByLabel('DAG Name').fill(dagName);
    await dialog.getByRole('button', { name: 'Create' }).click();

    await expect(page).toHaveURL(new RegExp(`/dags/${dagName}/spec$`));
    await expect(
      page.getByRole('heading', { level: 1, name: dagName, exact: true })
    ).toBeVisible();
  });

  test('saves harness warnings and clears them after configuring a directory', async ({ page, request }) => {
    const stack = await loadStack();
    const token = await loginViaAPI(
      request,
      stack.auth.adminUsername,
      stack.auth.adminPassword
    );
    const dagName = uniqueName('e2e-harness-warning');
    const definition = `name: ${dagName}
steps:
  - id: review
    action: harness.run
    with:
      provider: claude
      prompt: Review this repository.
`;
    const fileName = await writeLocalDAG(dagName, definition);
    await waitForDAGAvailable(request, token, fileName);
    await page.goto(`/dags/${encodeURIComponent(fileName)}/spec`);

    const warning = page
      .getByRole('status')
      .filter({ hasText: 'has no explicit working_dir' });
    const saveButton = page.getByRole('button', { name: 'Save', exact: true });
    const saveSpec = async () => {
      const response = page.waitForResponse(
        (response) =>
          response.request().method() === 'PUT' &&
          new URL(response.url()).pathname.endsWith('/spec')
      );
      await saveButton.click();
      expect((await response).ok()).toBeTruthy();
      await expect(saveButton).toBeDisabled();
    };
    await expect(warning).toBeVisible();
    const editor = page.locator('.monaco-editor textarea').first();
    await editor.focus();
    await page.keyboard.press('ControlOrMeta+End');
    await page.keyboard.insertText('\n# edited\n');
    await expect(page.getByText('Valid with warnings', { exact: true })).toBeVisible();
    await saveSpec();
    await expect(warning).toBeVisible();

    await editor.focus();
    await page.keyboard.press('ControlOrMeta+Home');
    await page.keyboard.insertText('working_dir: ./repo\n');
    await expect(page.getByText('Valid', { exact: true })).toBeVisible();
    await expect(warning).toHaveCount(0);
    await saveSpec();
    await page.reload();
    await expect(page.locator('.monaco-editor')).toBeVisible();
    await expect(warning).toHaveCount(0);
  });

  // Live validation redraws the graph, step table and errors above the
  // editor. None of that may move the editor while the user types.
  test('keeps the spec editor in place while the preview updates', async ({ page, request }) => {
    const stack = await loadStack();
    const token = await loginViaAPI(
      request,
      stack.auth.adminUsername,
      stack.auth.adminPassword
    );
    const dagName = uniqueName('e2e-spec-anchor');
    const definition = `name: ${dagName}
steps:
  - name: first
    run: echo first
`;
    const fileName = await writeLocalDAG(dagName, definition);
    await waitForDAGAvailable(request, token, fileName);
    await page.goto(`/dags/${encodeURIComponent(fileName)}/spec`);

    const graph = page.locator('.mermaid svg');
    const monaco = page.locator('.monaco-editor').first();
    await expect(graph.getByText('first', { exact: true })).toBeVisible();
    await expect(monaco).toBeVisible();
    // Leave the bottom of the preview in view, where browser scroll
    // anchoring alone would pin the preview rather than the editor.
    await page
      .getByRole('heading', { name: 'YAML', exact: true })
      .evaluate((heading) => {
        heading.scrollIntoView({ block: 'start' });
        let scroller = heading.parentElement;
        while (scroller && getComputedStyle(scroller).overflowY !== 'auto') {
          scroller = scroller.parentElement;
        }
        scroller?.scrollBy(0, -150);
      });
    const editorTop = async () => (await monaco.boundingBox())?.y ?? NaN;
    const initialTop = await editorTop();
    // Scroll positions snap to whole pixels while preview heights do not.
    const expectEditorInPlace = async () =>
      expect(Math.abs((await editorTop()) - initialTop)).toBeLessThan(2);

    // Single-line flow YAML sidesteps Monaco's auto-indent on typed newlines.
    // Brackets Monaco auto-closes and does not overtype end up after the
    // cursor, so the rest of the line is dropped. Select-all right after
    // focusing is occasionally lost, so the replacement is retried until the
    // buffer is that single line.
    const replaceSpec = async (spec: string) => {
      await expect(async () => {
        await page.keyboard.press('ControlOrMeta+A');
        await page.keyboard.insertText(spec);
        await page.keyboard.press('Shift+End');
        await page.keyboard.press('Delete');
        await expect(monaco.locator('.view-line')).toHaveCount(1, {
          timeout: 500,
        });
      }).toPass();
    };
    const step = (name: string, extra = '') =>
      `{name: ${name}, run: echo ${name}${extra}}`;
    await page.locator('.monaco-editor textarea').first().focus();
    await expect(monaco).toHaveClass(/\bfocused\b/);

    await replaceSpec('steps: []');
    await expect(page.getByText('No steps to render')).toBeVisible();
    await expectEditorInPlace();

    await replaceSpec(`steps: [${step('first')}, ${step('second')}, ${step('third')}]`);
    await expect(page.getByText('Valid', { exact: true })).toBeVisible();
    await expect(graph.getByText('third', { exact: true })).toBeVisible();
    await expectEditorInPlace();

    await replaceSpec(
      `steps: [${step('first')}, ${step('second')}, ` +
        `${step('third', ', depends: [missing]')}]`
    );
    await expect(page.getByText(/^\d+ issues?$/)).toBeVisible();
    await expectEditorInPlace();
  });

  test('renames a DAG from the UI', async ({ page, request }) => {
    const stack = await loadStack();
    const token = await loginViaAPI(
      request,
      stack.auth.adminUsername,
      stack.auth.adminPassword
    );

    const dagName = uniqueName('e2e-rename');
    const newName = uniqueName('e2e-renamed');
    const fileName = await writeLocalDAG(
      dagName,
      `
name: ${dagName}
steps:
  - name: echo
    run: echo "rename test"
`
    );
    await waitForDAGAvailable(request, token, fileName);

    await page.goto(`/dags/${encodeURIComponent(fileName)}/spec`);
    await expect(
      page.getByRole('heading', { level: 1, name: dagName, exact: true })
    ).toBeVisible();
    await page.getByRole('button', { name: 'Rename', exact: true }).click();

    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    await dialog.getByLabel('DAG Name').clear();
    await dialog.getByLabel('DAG Name').fill(newName);
    await dialog.getByRole('button', { name: 'Rename' }).click();

    await expect(page).toHaveURL(
      localScopedURL(
        stack.local.baseURL,
        `/dags/${encodeURIComponent(newName)}`
      )
    );
  });

  test('deletes a DAG from the UI', async ({ page, request }) => {
    const stack = await loadStack();
    const token = await loginViaAPI(
      request,
      stack.auth.adminUsername,
      stack.auth.adminPassword
    );

    const dagName = uniqueName('e2e-delete');
    const fileName = await writeLocalDAG(
      dagName,
      `
name: ${dagName}
steps:
  - name: echo
    run: echo "delete test"
`
    );
    await waitForDAGAvailable(request, token, fileName);

    await page.goto(`/dags/${encodeURIComponent(fileName)}/spec`);
    await expect(
      page.getByRole('heading', { level: 1, name: dagName, exact: true })
    ).toBeVisible();

    await page.getByRole('button', { name: 'Delete', exact: true }).click();

    const deleteDialog = page.getByRole('dialog');
    await expect(deleteDialog).toBeVisible();
    await expect(deleteDialog).toContainText('Delete DAG');
    await expect(deleteDialog).toContainText(fileName);
    await deleteDialog
      .getByRole('button', { name: 'Delete', exact: true })
      .click();

    await expect(page).toHaveURL(localScopedURL(stack.local.baseURL, '/dags'));

    // Verify DAG is gone via API
    const response = await request.get(
      `/api/v1/dags/${encodeURIComponent(fileName)}?remoteNode=local`,
      {
        headers: {
          Authorization: `Bearer ${token}`,
          'Content-Type': 'application/json',
        },
      }
    );
    expect(response.ok()).toBeFalsy();
  });

  test('suspends and resumes a DAG schedule', async ({ page, request }) => {
    const stack = await loadStack();
    const token = await loginViaAPI(
      request,
      stack.auth.adminUsername,
      stack.auth.adminPassword
    );

    const dagName = uniqueName('e2e-suspend');
    const fileName = await writeLocalDAG(
      dagName,
      `
name: ${dagName}
schedule: "0 0 * * *"
steps:
  - name: echo
    run: echo "suspend test"
`
    );
    await waitForDAGAvailable(request, token, fileName);

    await page.goto('/dags/');
    const dagEntry = dagDefinitionsEntry(page, dagName);
    await expect(dagEntry).toBeVisible();
    const scheduleToggle = dagEntry.getByRole('switch').first();
    await expect(scheduleToggle).toBeVisible();

    // DAG starts active — click switch to disable schedule
    await scheduleToggle.click();
    const disableDialog = page.getByRole('dialog');
    await expect(disableDialog).toBeVisible();
    await expect(disableDialog).toContainText('Disable Schedule');
    await disableDialog.getByRole('button', { name: 'Disable' }).click();
    await expect(disableDialog).toBeHidden();

    // Verify suspended via the canonical DAG details route the UI uses
    await expect
      .poll(
        async () => {
          const resp = await request.get(
            `/api/v1/dags/${encodeURIComponent(dagName)}?remoteNode=local`,
            {
              headers: {
                Authorization: `Bearer ${token}`,
                'Content-Type': 'application/json',
              },
            }
          );
          const body = (await resp.json()) as { suspended: boolean };
          return body.suspended;
        },
        { timeout: 15_000 }
      )
      .toBeTruthy();

    // Click switch again to re-enable schedule
    await scheduleToggle.click();
    const enableDialog = page.getByRole('dialog');
    await expect(enableDialog).toBeVisible();
    await expect(enableDialog).toContainText('Enable Schedule');
    await enableDialog.getByRole('button', { name: 'Enable' }).click();
    await expect(enableDialog).toBeHidden();

    // Verify resumed via the canonical DAG details route the UI uses
    await expect
      .poll(
        async () => {
          const resp = await request.get(
            `/api/v1/dags/${encodeURIComponent(dagName)}?remoteNode=local`,
            {
              headers: {
                Authorization: `Bearer ${token}`,
                'Content-Type': 'application/json',
              },
            }
          );
          const body = (await resp.json()) as { suspended: boolean };
          return body.suspended;
        },
        { timeout: 15_000 }
      )
      .toBeFalsy();
  });

  test('rejects DAG creation with invalid name', async ({ page }) => {
    await page.goto('/dags/');
    await page.getByRole('button', { name: 'Create new DAG' }).click();

    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    await dialog.getByLabel('DAG Name').fill('invalid name with spaces!');
    await dialog.getByRole('button', { name: 'Create' }).click();

    // Dialog stays open — validation prevents creation
    await expect(dialog).toBeVisible();
    await expect(page).not.toHaveURL(/\/spec$/);
  });
});

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
    await dialog.getByLabel('Workflow name').fill(dagName);
    await dialog.getByRole('button', { name: 'Create' }).click();

    // The workflow page is the workspace; the new file opens in its editor.
    await expect
      .poll(() => new URL(page.url()).pathname)
      .toMatch(new RegExp(`^/dags/${dagName}(/spec)?$`));
    await expect(page.getByTestId('ww-bar')).toBeVisible();
    await expect(
      page.getByRole('heading', { level: 1, name: dagName, exact: true })
    ).toBeVisible();
  });

  test('saves harness warnings and clears them after configuring a directory', async ({
    page,
    request,
  }) => {
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
    await page.goto(`/dags/${encodeURIComponent(fileName)}`);

    // Warnings do not block a save: Save carries an amber count of them.
    const warningCount = page.getByTestId('save-warning-count');
    const expectWarning = async (shown: boolean) => {
      await expect(warningCount).toHaveCount(shown ? 1 : 0, {
        timeout: 30_000,
      });
    };
    const saveButton = page.getByRole('button', { name: 'Save', exact: true });
    const saveSpec = async () => {
      const response = page.waitForResponse(
        (response) =>
          response.request().method() === 'PUT' &&
          new URL(response.url()).pathname.endsWith('/spec')
      );
      await saveButton.click();
      // The editor shows the YAML diff before it saves.
      await page
        .getByRole('dialog', { name: 'Review changes' })
        .getByRole('button', { name: 'Save', exact: true })
        .click();
      expect((await response).ok()).toBeTruthy();
      await expect(saveButton).toBeDisabled();
    };
    await expectWarning(true);
    // The warnings stay listed while the YAML drawer is open.
    await page.getByRole('button', { name: 'YAML', exact: true }).click();
    await expectWarning(true);
    const editor = page.locator('.monaco-editor textarea').first();
    await editor.focus();
    await page.keyboard.press('ControlOrMeta+End');
    await page.keyboard.insertText('\n# edited\n');
    await expect(
      page.getByText('Valid with warnings', { exact: true })
    ).toBeVisible();
    await saveSpec();
    await expectWarning(true);

    await editor.focus();
    await page.keyboard.press('ControlOrMeta+Home');
    await page.keyboard.insertText('working_dir: ./repo\n');
    await expect(page.getByText('Valid', { exact: true })).toBeVisible();
    await expectWarning(false);
    await saveSpec();
    await page.reload();
    await expectWarning(false);
  });

  // Live validation redraws the canvas and the messages. None of that may move
  // the YAML drawer while the user types.
  test('keeps the spec editor in place while the canvas updates', async ({
    page,
    request,
  }) => {
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
    await page.setViewportSize({ width: 1600, height: 900 });
    await page.goto(`/dags/${encodeURIComponent(fileName)}`);

    await page.getByRole('button', { name: 'YAML', exact: true }).click();
    const monaco = page.locator('.monaco-editor').first();
    await expect(monaco).toBeInViewport();
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

    await replaceSpec(
      `steps: [${step('first')}, ${step('second')}, ${step('third')}]`
    );
    await expect(page.getByText('Valid', { exact: true })).toBeVisible();
    await expect(page.getByTestId('ve-canvas-node-steps[2]')).toBeVisible();
    await expectEditorInPlace();

    await replaceSpec(
      `steps: [${step('first')}, ${step('second')}, ` +
        `${step('third', ', depends: [missing]')}]`
    );
    await expect(page.getByText(/^\d+ issues?$/).first()).toBeAttached();
    await expectEditorInPlace();
  });

  // Rename and delete from the workflow bar: e2e/workspace/shell.spec.ts.

  // Pins are shared by every user of the server, so the test always unpins.
  test('pins and unpins a workflow', async ({ page, request }) => {
    const stack = await loadStack();
    const token = await loginViaAPI(
      request,
      stack.auth.adminUsername,
      stack.auth.adminPassword
    );
    const headers = { Authorization: `Bearer ${token}` };

    const dagName = uniqueName('e2e-pin');
    const fileName = await writeLocalDAG(
      dagName,
      `
name: ${dagName}
steps:
  - name: echo
    run: echo "pin test"
`
    );
    await waitForDAGAvailable(request, token, fileName);
    const isPinned = async () => {
      const resp = await request.get(
        `/api/v1/dags/${encodeURIComponent(fileName)}?remoteNode=local`,
        { headers }
      );
      return ((await resp.json()) as { pinned: boolean }).pinned;
    };

    try {
      await page.goto('/dags/');
      const dagEntry = dagDefinitionsEntry(page, dagName);
      await expect(dagEntry).toBeVisible();
      await dagEntry
        .getByRole('button', { name: `Pin workflow ${dagName}` })
        .click();
      await expect.poll(isPinned, { timeout: 15_000 }).toBe(true);
      await expect(
        page.getByText('Pinned', { exact: true }).first()
      ).toBeVisible();

      await page.goto(`/dags/${encodeURIComponent(fileName)}`);
      const headerPin = page.getByRole('button', {
        name: `Pin workflow ${dagName}`,
      });
      await expect(headerPin).toHaveAttribute('aria-pressed', 'true');
      await headerPin.click();
      await expect.poll(isPinned, { timeout: 15_000 }).toBe(false);
    } finally {
      await request.delete(
        `/api/v1/dags/${encodeURIComponent(fileName)}/pin?remoteNode=local`,
        { headers }
      );
    }
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
    await expect(disableDialog).toContainText('Disable schedule');
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
    await expect(enableDialog).toContainText('Enable schedule');
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
    await dialog.getByLabel('Workflow name').fill('invalid name with spaces!');
    await dialog.getByRole('button', { name: 'Create' }).click();

    // Dialog stays open — validation prevents creation
    await expect(dialog).toBeVisible();
    await expect(page).not.toHaveURL(/\/spec$/);
  });
});

// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { writeFile } from 'node:fs/promises';
import path from 'node:path';
import type { Locator, Page } from '@playwright/test';
import {
  createUser,
  hasRBACLicenseSourceConfigured,
  loadStack,
  loginViaAPI,
  loginViaUI,
  startDAG,
  uniqueName,
  useDefaultWorkspaceScope,
  waitForDAGAvailable,
  waitForRunStatus,
  writeLocalDAG,
  type UserRole,
} from '../helpers/e2e';
import { test, expect } from './fixtures';

// parity:test-e2e-auth parity:canvas-readonly-chip parity:edit-permission
// parity:actions-permission-gate - each gated item of the workspace for each
// role, as e2e/auth-permissions.spec.ts does for the routes.

interface RoleGates {
  /** useCanWriteForWorkspace: edit, Save, add a step, Rename, Delete. */
  canWrite: boolean;
  /** useCanExecute: the unsaved draft run. */
  canExecute: boolean;
  /** useCanManageProfiles: the default profile of the workflow. */
  canManageProfiles: boolean;
  isAdmin: boolean;
}

// The gates of useWorkspacePermissions for a user with access to every
// workspace. canRunDags is the server permission run_dags, true on the stack
// for every role.
const ROLES: ReadonlyArray<{ role: UserRole; gates: RoleGates }> = [
  {
    role: 'viewer',
    gates: {
      canWrite: false,
      canExecute: false,
      canManageProfiles: false,
      isAdmin: false,
    },
  },
  {
    role: 'operator',
    gates: {
      canWrite: false,
      canExecute: true,
      canManageProfiles: false,
      isAdmin: false,
    },
  },
  {
    role: 'developer',
    gates: {
      canWrite: true,
      canExecute: true,
      canManageProfiles: false,
      isAdmin: false,
    },
  },
  {
    role: 'manager',
    gates: {
      canWrite: true,
      canExecute: true,
      canManageProfiles: true,
      isAdmin: false,
    },
  },
  {
    role: 'admin',
    gates: {
      canWrite: true,
      canExecute: true,
      canManageProfiles: true,
      isAdmin: true,
    },
  },
];

function canvas(page: Page): Locator {
  return page.getByTestId('ww-canvas');
}

async function expectPresent(locator: Locator, present: boolean) {
  if (present) {
    await expect(locator).toBeVisible();
  } else {
    await expect(locator).toHaveCount(0);
  }
}

test.describe('workspace roles', () => {
  test.skip(
    !hasRBACLicenseSourceConfigured(),
    'requires a Dagu Pro license source for RBAC user management'
  );
  // The run actions of the bar show from the md breakpoint.
  test.skip(
    ({ viewport }) => (viewport?.width ?? 0) < 768,
    'desktop layout only'
  );

  for (const { role, gates } of ROLES) {
    test(`gives the ${role} role only its items`, async ({
      page,
      request,
      gotoWorkspace,
    }) => {
      test.setTimeout(150_000);
      const stack = await loadStack();
      const adminToken = await loginViaAPI(
        request,
        stack.auth.adminUsername,
        stack.auth.adminPassword
      );
      const username = uniqueName(`ws-${role}`);
      const password = `${role}-pass-123`;
      await createUser(request, adminToken, { username, password, role });

      // A running run, so that Stop exists for the roles that may use it. The
      // API refuses a file name of more than 40 characters, '.yaml' included,
      // and uniqueName adds 16: the prefix stays short.
      const dagName = uniqueName(`e2e-ws-r-${role}`);
      const release = path.join(stack.stateDir, `${dagName}.release`);
      const spec = `
name: ${dagName}
steps:
  - name: hold
    run: |
      while [ ! -f "${release}" ]; do sleep 0.2; done
      echo released
`;
      const fileName = await writeLocalDAG(dagName, spec);

      try {
        await waitForDAGAvailable(request, adminToken, fileName);
        const runId = await startDAG(request, adminToken, fileName);
        await waitForRunStatus(
          request,
          adminToken,
          dagName,
          runId,
          ['running'],
          'local',
          30_000
        );
        // The saved definition changes after the run started: the run used
        // another definition.
        await writeLocalDAG(
          dagName,
          `description: changed after the run\n${spec}`
        );
        await waitForDAGAvailable(request, adminToken, fileName);

        await loginViaUI(page, username, password);
        await useDefaultWorkspaceScope(page);
        await gotoWorkspace(page, `/dags/${encodeURIComponent(fileName)}`);
        await expect(page.getByTestId('workflow-workspace')).toBeVisible({
          timeout: 30_000,
        });
        await expect(
          canvas(page).getByTestId('ve-canvas-node-steps[0]')
        ).toBeVisible({ timeout: 30_000 });
        // The one tab shows the run chip and the run icons of the run that
        // the workflow has. The run used another definition than the saved
        // one, so the chip has the stale dot.
        await expect(page.getByTestId('ww-run-chip')).toBeVisible({
          timeout: 30_000,
        });
        await expect(page.getByTestId('ww-run-stale-dot')).toBeVisible({
          timeout: 30_000,
        });

        // Canvas: the read-only mark and Save follow canWrite; the "Add a
        // step" end button follows it too.
        await expectPresent(
          canvas(page).getByText('Read-only', { exact: true }),
          !gates.canWrite
        );
        const addStep = canvas(page)
          .getByRole('button', { name: 'Add a step' })
          .first();
        if (gates.canWrite) {
          await expect(addStep).toBeEnabled();
          await expect(page.locator('#save-config')).toBeVisible();
        } else {
          await expect(addStep).toHaveCount(0);
          await expect(page.locator('#save-config')).toHaveCount(0);
        }

        // Run controls. Start is on the bar. Stop is one of the flat run
        // icons. Both are gated by the server permission run_dags, which
        // this stack gives to every role; the viewer has them too, so they
        // are checked from operator up.
        if (gates.canExecute) {
          await expect(
            page.getByTestId('ww-run-split').locator('[data-ww-action="start"]')
          ).toBeVisible();
          await expect(
            page
              .getByTestId('ww-run-actions')
              .getByRole('button', { name: 'Stop', exact: true })
          ).toBeVisible({ timeout: 30_000 });
        }
        // "Copy to editor" writes the editor: it needs write access, and the
        // stale run gives it.
        await expectPresent(
          page
            .getByTestId('ww-run-actions')
            .getByRole('button', { name: 'Copy to editor' }),
          gates.canWrite
        );
        // The run menu holds the unsaved draft only. A clean editor keeps
        // its caret in place, disabled, for a role that may execute.
        const runOptions = page
          .getByTestId('ww-run-split')
          .getByRole('button', { name: 'More run options' });
        if (gates.canExecute) {
          await expect(runOptions).toBeDisabled();
        } else {
          await expect(runOptions).toHaveCount(0);
        }

        // The bar has no menu: Rename and Delete are buttons that follow
        // canWrite, and no entry leads to the run page.
        const bar = page.getByTestId('ww-bar');
        await expect(
          bar.getByRole('button', { name: 'More actions' })
        ).toHaveCount(0);
        await expect(bar.getByText('Open run page')).toHaveCount(0);
        await expectPresent(
          bar.getByRole('button', { name: 'Rename', exact: true }),
          gates.canWrite
        );
        await expectPresent(
          bar.getByRole('button', { name: 'Delete', exact: true }),
          gates.canWrite
        );

        // Runtime profile, in the Overview tab of the Workflow panel: only a
        // profile manager can change the default profile.
        await gotoWorkspace(
          page,
          `/dags/${encodeURIComponent(fileName)}?panel=workflow&remoteNode=local`
        );
        const profile = page.getByTestId('ww-runtime-profile');
        await expect(profile).toBeVisible({ timeout: 30_000 });
        await expect(
          profile.getByText('Default profile', { exact: true })
        ).toBeVisible({ timeout: 30_000 });
        await expect(profile.locator('#dag-default-profile')).toHaveCount(
          gates.canManageProfiles ? 1 : 0
        );
        await expectPresent(
          profile.getByRole('link', { name: 'Manage profiles' }),
          gates.canManageProfiles
        );
      } finally {
        await writeFile(release, '');
        await request.delete(
          `/api/v1/dags/${encodeURIComponent(fileName)}?remoteNode=local`,
          { headers: { Authorization: `Bearer ${adminToken}` } }
        );
      }
    });
  }
});

// The server permission run_dags: the page reads it from the config that the
// server writes in the page. The stack gives it to every role, so the test
// turns it off in the served page.
test.describe('workspace without the run permission', () => {
  test.skip(
    ({ viewport }) => (viewport?.width ?? 0) < 768,
    'desktop layout only'
  );

  test('has no Run and no run action', async ({
    page,
    request,
    gotoWorkspace,
  }) => {
    test.setTimeout(150_000);
    const stack = await loadStack();
    const adminToken = await loginViaAPI(
      request,
      stack.auth.adminUsername,
      stack.auth.adminPassword
    );
    const dagName = uniqueName('e2e-ws-norun');
    const fileName = await writeLocalDAG(
      dagName,
      `
name: ${dagName}
steps:
  - name: hello
    run: echo hello
`
    );
    try {
      await waitForDAGAvailable(request, adminToken, fileName);
      const runId = await startDAG(request, adminToken, fileName);
      await waitForRunStatus(request, adminToken, dagName, runId, [
        'succeeded',
      ]);

      await page.route('**/*', async (route) => {
        if (route.request().resourceType() !== 'document') {
          await route.fallback();
          return;
        }
        const response = await route.fetch();
        const body = (await response.text()).replace(
          /runDags:\s*"true"\s*===\s*"true"/,
          'runDags: false'
        );
        await route.fulfill({ response, body });
      });
      await loginViaUI(
        page,
        stack.auth.adminUsername,
        stack.auth.adminPassword
      );
      await useDefaultWorkspaceScope(page);
      await gotoWorkspace(page, `/dags/${encodeURIComponent(fileName)}`);
      await expect(
        canvas(page).getByTestId('ve-canvas-node-steps[0]')
      ).toBeVisible({ timeout: 30_000 });
      // The run is still visible: the chip shows it.
      await expect(page.getByTestId('ww-run-chip')).toBeVisible({
        timeout: 30_000,
      });

      // No Run button, no run menu, and none of the run icons.
      const bar = page.getByTestId('ww-bar');
      await expect(page.getByTestId('ww-run-split')).toHaveCount(0);
      await expect(bar.locator('[data-ww-action="start"]')).toHaveCount(0);
      const actions = page.getByTestId('ww-run-actions');
      for (const name of ['Stop', 'Retry', 'Reschedule']) {
        await expect(
          actions.getByRole('button', { name, exact: true })
        ).toHaveCount(0);
      }
      await expect(bar.getByRole('button', { name: 'Start' })).toHaveCount(0);
    } finally {
      await request.delete(
        `/api/v1/dags/${encodeURIComponent(fileName)}?remoteNode=local`,
        { headers: { Authorization: `Bearer ${adminToken}` } }
      );
    }
  });
});

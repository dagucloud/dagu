// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { expect, test } from '@playwright/test';
import {
  loadStack,
  loginViaAPI,
  loginViaUI,
  uniqueName,
  useDefaultWorkspaceScope,
} from './helpers/e2e';

test('reorders sidebar bookmarks and keeps their order after reload', async ({
  page,
  request,
}) => {
  const stack = await loadStack();
  const token = await loginViaAPI(
    request,
    stack.auth.adminUsername,
    stack.auth.adminPassword
  );
  const headers = { Authorization: `Bearer ${token}` };
  const names: string[] = [];
  const ids: string[] = [];

  try {
    for (const type of ['kanban', 'run', 'workflow', 'artifact']) {
      const name = uniqueName(`bookmark-${type}`);
      const response = await request.post('/api/v1/views?remoteNode=local', {
        headers,
        data: {
          name,
          type,
          intervalDays: 1,
          workspaceScope: 'default',
          pinned: true,
        },
      });
      expect(response.ok()).toBeTruthy();
      ids.push((await response.json()).id);
      names.push(name);
    }

    await loginViaUI(page, stack.auth.adminUsername, stack.auth.adminPassword);
    await useDefaultWorkspaceScope(page);
    await page.goto('/dags');
    const sidebar = page.getByTestId('app-sidebar');
    const links = sidebar.getByRole('link').filter({ hasText: 'bookmark-' });
    await expect(links).toHaveText(names);
    const originalURL = page.url();
    const first = sidebar.getByRole('link', { name: names[0], exact: true });
    const last = sidebar.getByRole('link', { name: names[3], exact: true });

    await last.dragTo(first);
    const reordered = [names[3]!, ...names.slice(0, 3)];
    await expect(links).toHaveText(reordered);
    await expect(page).toHaveURL(originalURL);
    await page.reload();
    await expect(links).toHaveText(reordered);

    await last.dragTo(
      sidebar.getByRole('link', { name: names[2], exact: true })
    );
    await expect(links).toHaveText(names);
    await first.focus();
    await first.press('Alt+ArrowDown');
    await expect(links).toHaveText([
      names[1]!,
      names[0]!,
      names[2]!,
      names[3]!,
    ]);
    await expect(first).toBeFocused();
    await first.click();
    await expect(page).toHaveURL(new RegExp(`/views/${ids[0]}`));
  } finally {
    for (const id of ids) {
      await request.delete(`/api/v1/views/${id}?remoteNode=local`, { headers });
    }
  }
});

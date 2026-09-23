// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { expect, test } from '@playwright/test';
import {
  getStepStdout,
  loadStack,
  loginViaAPI,
  loginViaUI,
  startDAG,
  uniqueName,
  waitForDAGAvailable,
  waitForRunStatus,
  writeLocalDAG,
} from './helpers/e2e';

test.describe('Human task push-back', () => {
  test('sends a review back with feedback and completes the reopened task', async ({
    page,
    request,
  }) => {
    const stack = await loadStack();
    await loginViaUI(page, stack.auth.adminUsername, stack.auth.adminPassword);
    const token = await loginViaAPI(request, stack.auth.adminUsername, stack.auth.adminPassword);
    const dagName = uniqueName('e2e-push-back');
    const fileName = await writeLocalDAG(dagName, `
name: ${dagName}
steps:
  - id: implement
    run: echo "attempt=$DAG_PUSHBACK_ITERATION feedback=$feedback"
  - id: review
    depends: implement
    action: human.task
    with:
      prompt: Review the implementation
      push_back:
        rewind_to: implement
        form:
          type: object
          required: [feedback]
          properties:
            feedback:
              type: string
              title: Feedback
  - id: publish
    depends: review
    run: echo published
`);
    await waitForDAGAvailable(request, token, fileName);
    const runId = await startDAG(request, token, fileName);
    await waitForRunStatus(request, token, dagName, runId, ['waiting']);

    await page.goto(`/dag-runs/${encodeURIComponent(dagName)}/${encodeURIComponent(runId)}`);
    await expect(page.getByText('Review the implementation')).toBeVisible();
    await page.getByRole('button', { name: 'Request changes' }).click();
    await expect(
      page.getByText(
        'implement and the steps that depend on it run again with your feedback. This task reopens afterward.'
      )
    ).toBeVisible();
    await page.getByLabel(/Feedback/).fill('Add tests');
    await page.getByRole('button', { name: 'Request changes' }).click();

    // The rewound step runs again with the feedback before the task reopens.
    await expect
      .poll(async () =>
        getStepStdout(request, token, dagName, runId, 'implement', { retryNotFound: true })
      )
      .toContain('attempt=1 feedback=Add tests');
    await waitForRunStatus(request, token, dagName, runId, ['waiting']);

    await expect(page.getByText('Previous Push-backs')).toBeVisible();
    await expect(page.getByText('feedback="Add tests"').first()).toBeVisible();
    await page.getByRole('button', { name: 'Complete task' }).click();

    await waitForRunStatus(request, token, dagName, runId, ['succeeded']);
    expect(await getStepStdout(request, token, dagName, runId, 'publish')).toContain('published');
  });
});

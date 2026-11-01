// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { useEffect } from 'react';
import {
  BrowserRouter,
  Link,
  Route,
  Routes,
  useLocation,
} from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  useWorkspaceEditorBinding,
  WorkspaceEditorProvider,
} from '../../canvas/WorkspaceEditorContext';
import type { WorkspaceEditorState } from '../../contracts';
import { EditorLeaveGuard } from '../EditorLeaveGuard';
import {
  SettingsLeaveGuardProvider,
  useSettingsLeaveGuard,
} from '../useSettingsLeaveGuard';

function Editor({ dirty }: { dirty: boolean }) {
  const { onEditorStateChange } = useWorkspaceEditorBinding();
  useEffect(() => {
    onEditorStateChange({ dirty } as WorkspaceEditorState);
  }, [dirty, onEditorStateChange]);
  return null;
}

// A section of the Workflow panel with a draft that is not saved.
function SettingsDraft() {
  const { setDirty } = useSettingsLeaveGuard();
  useEffect(() => setDirty('notifications', true), [setDirty]);
  return null;
}

function Where() {
  const { pathname } = useLocation();
  return <output data-testid="where">{pathname}</output>;
}

function Page({
  dirty,
  settingsDirty,
}: {
  dirty: boolean;
  settingsDirty: boolean;
}) {
  return (
    <WorkspaceEditorProvider>
      <SettingsLeaveGuardProvider>
        <Editor dirty={dirty} />
        {settingsDirty && <SettingsDraft />}
        <EditorLeaveGuard />
        <Link to="/dags/etl/executions">in workflow</Link>
        <Link to="/dag-runs">leave</Link>
        <a href="https://example.com/x">external</a>
      </SettingsLeaveGuardProvider>
    </WorkspaceEditorProvider>
  );
}

function renderPage(dirty: boolean, settingsDirty = false) {
  const page = <Page dirty={dirty} settingsDirty={settingsDirty} />;
  return render(
    <BrowserRouter>
      <Where />
      <Routes>
        <Route path="/dags/:fileName" element={page} />
        <Route path="/dags/:fileName/:tab" element={page} />
        <Route path="*" element={<div>elsewhere</div>} />
      </Routes>
    </BrowserRouter>
  );
}

// The router starts at index 0; the browser goes back to the entry at index 5
// of another page.
function backToAnotherPage() {
  act(() => {
    window.history.replaceState({ idx: 5 }, '', '/dag-runs');
    window.dispatchEvent(new PopStateEvent('popstate'));
  });
}

function unloadPrevented() {
  const event = new Event('beforeunload', { cancelable: true });
  window.dispatchEvent(event);
  return event.defaultPrevented;
}

beforeEach(() => {
  window.history.replaceState(null, '', '/dags/etl');
});
afterEach(() => {
  vi.restoreAllMocks();
  window.history.replaceState(null, '', '/');
});

describe('EditorLeaveGuard', () => {
  it('asks on unload only while the draft is dirty', () => {
    const { unmount } = renderPage(false);
    expect(unloadPrevented()).toBe(false);
    unmount();
    renderPage(true);
    expect(unloadPrevented()).toBe(true);
  });

  it('asks before a link that leaves the workflow, then follows it on confirm', async () => {
    const user = userEvent.setup();
    renderPage(true);

    await user.click(screen.getByRole('link', { name: 'leave' }));
    expect(screen.getByTestId('where')).toHaveTextContent('/dags/etl');
    const dialog = await screen.findByRole('dialog');
    expect(dialog).toHaveTextContent('The workflow has unsaved changes');

    await user.click(screen.getByRole('button', { name: 'Leave' }));
    await waitFor(() =>
      expect(screen.getByTestId('where')).toHaveTextContent('/dag-runs')
    );
  });

  it('stays on the page when the question is cancelled', async () => {
    const user = userEvent.setup();
    renderPage(true);
    await user.click(screen.getByRole('link', { name: 'leave' }));
    await user.click(await screen.findByRole('button', { name: 'Cancel' }));
    expect(screen.getByTestId('where')).toHaveTextContent('/dags/etl');
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    );
  });

  it('lets a link inside the workflow and a clean draft pass', async () => {
    const user = userEvent.setup();
    renderPage(true);
    await user.click(screen.getByRole('link', { name: 'in workflow' }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(screen.getByTestId('where')).toHaveTextContent(
      '/dags/etl/executions'
    );
  });

  it('does not ask for a clean draft', async () => {
    const user = userEvent.setup();
    renderPage(false);
    await user.click(screen.getByRole('link', { name: 'leave' }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(screen.getByTestId('where')).toHaveTextContent('/dag-runs');
  });

  it('holds back a Back to another page and asks first', async () => {
    const go = vi.spyOn(window.history, 'go').mockImplementation(() => {});
    renderPage(true);
    backToAnotherPage();
    // The router never saw the event, so it still shows the workflow.
    expect(screen.getByTestId('where')).toHaveTextContent('/dags/etl');
    expect(await screen.findByRole('dialog')).toHaveTextContent(
      'The workflow has unsaved changes'
    );
    // The guard undoes the Back, and replays it once the user confirms.
    expect(go.mock.calls).toEqual([[-5]]);
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: 'Leave' }));
    expect(go.mock.calls).toEqual([[-5], [5]]);
  });

  it('does not replay a held Back when the question is cancelled', async () => {
    const go = vi.spyOn(window.history, 'go').mockImplementation(() => {});
    renderPage(true);
    backToAnotherPage();
    await userEvent
      .setup()
      .click(await screen.findByRole('button', { name: 'Cancel' }));
    expect(go.mock.calls).toEqual([[-5]]);
  });

  // Leaving the workflow page also drops the drafts of the Workflow panel.
  it('asks before a link that leaves the workflow with a settings draft', async () => {
    const user = userEvent.setup();
    renderPage(false, true);
    await user.click(screen.getByRole('link', { name: 'leave' }));
    expect(screen.getByTestId('where')).toHaveTextContent('/dags/etl');
    expect(await screen.findByRole('dialog')).toHaveTextContent(
      'The changes in this section are not saved'
    );
  });

  it('holds back a Back to another page with a settings draft', async () => {
    const go = vi.spyOn(window.history, 'go').mockImplementation(() => {});
    renderPage(false, true);
    backToAnotherPage();
    expect(screen.getByTestId('where')).toHaveTextContent('/dags/etl');
    expect(await screen.findByRole('dialog')).toHaveTextContent(
      'The changes in this section are not saved'
    );
    expect(go.mock.calls).toEqual([[-5]]);
  });

  it('lets a Back inside the workflow pass', () => {
    renderPage(true);
    act(() => {
      window.history.replaceState({ idx: 5 }, '', '/dags/etl/executions');
      window.dispatchEvent(new PopStateEvent('popstate'));
    });
    expect(screen.getByTestId('where')).toHaveTextContent(
      '/dags/etl/executions'
    );
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });
});

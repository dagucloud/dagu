// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, render } from '@testing-library/react';
import { MemoryRouter, useLocation, useNavigationType } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { WorkspaceEditorHandle } from '../../contracts';
import { useOpenStepDetails } from '../useOpenStepDetails';

const state = vi.hoisted(() => ({
  handle: null as null | Partial<WorkspaceEditorHandle>,
}));
const dock = vi.hoisted(() => ({
  toggle: vi.fn(),
  openStep: vi.fn(),
  setTab: vi.fn(),
}));
vi.mock('../WorkspaceEditorContext', () => ({
  useWorkspaceEditor: () => ({ handle: state.handle, state: null }),
}));
vi.mock('../../dock/DockContext', () => ({
  useDock: () => dock,
  useDockCommands: () => dock,
}));

let open: (step: string) => void;
let openYaml: (step: string) => void;
let search = '';
let navigation = '';

function Probe() {
  open = useOpenStepDetails();
  openYaml = useOpenStepDetails({ form: false });
  search = useLocation().search;
  navigation = useNavigationType();
  return null;
}

beforeEach(() => sessionStorage.clear());

function mount(url: string) {
  render(
    <MemoryRouter initialEntries={[url]}>
      <Probe />
    </MemoryRouter>
  );
}

describe('useOpenStepDetails', () => {
  // The step view opens on the step with replace, and the Run panel stays
  // as it is: a pick never opens it.
  it('selects the step, opens its view and leaves the dock alone', () => {
    state.handle = {
      openForm: vi.fn(() => true),
      openYaml: vi.fn(() => true),
      revealStep: vi.fn(),
    };
    mount('/dags/example.yaml?run=run-1&dock=log');
    act(() => open('build'));
    expect(search).toBe('?run=run-1&step=build&ndv=expanded&dock=log');
    expect(navigation).toBe('REPLACE');
    expect(dock.toggle).not.toHaveBeenCalled();
    expect(dock.openStep).not.toHaveBeenCalled();
    expect(dock.setTab).not.toHaveBeenCalled();
  });

  // The step opens on its YAML, as a double click does; after the form, the
  // form is the last mode of the session.
  it('opens the YAML at first and the form after the form', () => {
    const openForm = vi.fn(() => true);
    const openYaml = vi.fn(() => true);
    state.handle = { openForm, openYaml, revealStep: vi.fn() };
    mount('/dags/example.yaml');
    act(() => open('build'));
    expect(openYaml).toHaveBeenCalledWith('build');
    expect(openForm).not.toHaveBeenCalled();

    sessionStorage.setItem('dagu.stepView.mode', 'form');
    act(() => open('test'));
    expect(openForm).toHaveBeenCalledWith('test');
  });

  it('reveals the step when the editor has no such step', () => {
    const revealStep = vi.fn();
    state.handle = {
      openForm: vi.fn(() => false),
      openYaml: vi.fn(() => false),
      revealStep,
    };
    mount('/dags/example.yaml');
    act(() => open('build'));
    expect(revealStep).toHaveBeenCalledWith('build');
  });

  // A link in the Upstream data column keeps the tab of the view: it selects
  // the step and never opens its form.
  it('selects the step without opening its form with form false', () => {
    const openForm = vi.fn(() => true);
    const revealStep = vi.fn();
    state.handle = { openForm, revealStep };
    mount('/dags/example.yaml?run=run-1&remoteNode=edge');
    act(() => openYaml('build'));
    expect(openForm).not.toHaveBeenCalled();
    expect(revealStep).toHaveBeenCalledWith('build');
    expect(Object.fromEntries(new URLSearchParams(search))).toEqual({
      run: 'run-1',
      remoteNode: 'edge',
      step: 'build',
      ndv: 'expanded',
    });
  });

  it('only writes the URL without a bound editor', () => {
    state.handle = null;
    mount('/dags/example.yaml');
    act(() => open('build'));
    expect(search).toBe('?step=build&ndv=expanded');
  });
});

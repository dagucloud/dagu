// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { describe, expect, it } from 'vitest';
import { RemoteNodeProvider } from '@/contexts/RemoteNodeContext';
import type { ApiDAGDetails } from '../../contracts';
import { LabelChips } from '../LabelChips';

function renderChips(
  dag: Partial<ApiDAGDetails>,
  { url = '/dags/etl', remoteNode }: { url?: string; remoteNode?: string } = {}
) {
  const element = (
    <LabelChips
      fileName="etl"
      dag={{ name: 'etl', ...dag } as ApiDAGDetails}
      workspaceName={null}
    />
  );
  return render(
    <MemoryRouter initialEntries={[url]}>
      <RemoteNodeProvider remoteNode={remoteNode}>
        <Routes>
          <Route path="/dags/:fileName" element={element} />
          <Route path="/dags/:fileName/:tab" element={element} />
        </Routes>
      </RemoteNodeProvider>
    </MemoryRouter>
  );
}

function chipLinks() {
  return screen
    .getAllByRole('link')
    .map((link) => [link.textContent, link.getAttribute('href')]);
}

// parity:workflow-attributes
describe('LabelChips', () => {
  it('links each label and legacy tag to the filtered workflow list', () => {
    renderChips({ labels: ['team=data', 'nightly'], tags: ['legacy'] });
    expect(screen.getByRole('list', { name: 'Labels' })).toBeVisible();
    expect(chipLinks()).toEqual([
      ['team=data', '/dags?labels=team%3Ddata'],
      ['nightly', '/dags?labels=nightly'],
      ['legacy', '/dags?labels=legacy'],
    ]);
  });

  it('keeps the remote node in the links', () => {
    renderChips(
      { labels: ['nightly'] },
      { url: '/dags/etl?remoteNode=edge', remoteNode: 'edge' }
    );
    expect(chipLinks()).toEqual([
      ['nightly', '/dags?labels=nightly&remoteNode=edge'],
    ]);
  });

  it('hides workspace labels and repeated labels', () => {
    renderChips({
      labels: ['workspace=ops', 'nightly'],
      tags: ['nightly'],
    });
    expect(chipLinks()).toEqual([['nightly', '/dags?labels=nightly']]);
  });

  it('collapses the overflow into +n with a tooltip', async () => {
    renderChips({ labels: ['a', 'b', 'c', 'd', 'e'] });
    expect(chipLinks().map(([text]) => text)).toEqual(['a', 'b', 'c']);
    const more = screen.getByRole('button', { name: '2 more labels' });
    expect(more).toHaveTextContent('+2');

    fireEvent.focus(more);
    expect(await screen.findByRole('tooltip')).toHaveTextContent('d, e');
  });

  it('shows a single extra label instead of +1', () => {
    renderChips({ labels: ['a', 'b', 'c', 'd'] });
    expect(chipLinks().map(([text]) => text)).toEqual(['a', 'b', 'c', 'd']);
    expect(screen.queryByRole('button')).toBeNull();
  });

  it('renders nothing without labels', () => {
    const { container } = renderChips({ labels: ['workspace=ops'] });
    expect(within(container).queryByRole('list')).toBeNull();
  });
});

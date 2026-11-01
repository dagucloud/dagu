// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import type { CanvasGraph } from '../../contracts';
import { ExportMenu } from '../ExportMenu';

const exports = vi.hoisted(() => ({
  buildExportSvg: vi.fn(() => ({ svg: '<svg/>', width: 300, height: 120 })),
  downloadSvg: vi.fn(),
  downloadPng: vi.fn(() => Promise.resolve()),
}));
vi.mock('../exportCanvas', () => exports);

const graph: CanvasGraph = {
  nodes: [
    {
      key: 'a',
      label: 'a',
      kind: 'run',
      lane: 'main',
      issues: { errors: 0, warnings: 0 },
    },
  ],
  edges: [],
  editableEdges: true,
};

function renderMenu() {
  render(
    <UserPreferencesProvider>
      <ExportMenu
        graph={graph}
        positions={{ a: { x: 5, y: 6 } }}
        direction="TB"
        name="etl"
      />
    </UserPreferencesProvider>
  );
}

describe('ExportMenu', () => {
  beforeEach(() => vi.clearAllMocks());

  it('saves an SVG of the graph where the nodes are on screen', async () => {
    renderMenu();
    await userEvent.click(screen.getByRole('button', { name: 'Export graph' }));
    await userEvent.click(
      await screen.findByRole('menuitem', { name: 'Export as SVG' })
    );

    expect(exports.buildExportSvg).toHaveBeenCalledWith(
      graph,
      { a: { x: 5, y: 6 } },
      'TB',
      { dark: false, compact: false }
    );
    expect(exports.downloadSvg).toHaveBeenCalledWith('<svg/>', 'etl');
  });

  it('saves a PNG with the size of the drawing', async () => {
    renderMenu();
    await userEvent.click(screen.getByRole('button', { name: 'Export graph' }));
    fireEvent.click(
      await screen.findByRole('menuitem', { name: 'Export as PNG' })
    );

    expect(exports.downloadPng).toHaveBeenCalledWith('<svg/>', 300, 120, 'etl');
  });
});

// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import * as React from 'react';
import { describe, expect, it, vi } from 'vitest';
import { DocTreeNodeResponseType } from '@/api/v1/schema';
import DocArboristNode from '../docs/components/DocArboristNode';

function makeFileNode(workspace?: string | null) {
  const select = vi.fn();
  const activate = vi.fn();
  return {
    id: workspace ? `${workspace}/runbooks/deploy` : 'runbooks/deploy',
    data: {
      id: workspace ? `${workspace}/runbooks/deploy` : 'runbooks/deploy',
      name: 'deploy',
      title: 'deploy',
      type: DocTreeNodeResponseType.file,
      workspace,
    },
    isEditing: false,
    isSelected: false,
    isOpen: false,
    willReceiveDrop: false,
    isDragging: false,
    select,
    activate,
    selectMulti: vi.fn(),
    selectContiguous: vi.fn(),
    toggle: vi.fn(),
    submit: vi.fn(),
    reset: vi.fn(),
    edit: vi.fn(),
  };
}

describe('DocArboristNode', () => {
  it('activates a file once when the filename is clicked', async () => {
    const user = userEvent.setup();
    const node = makeFileNode();
    const defaultRowClick = vi.fn(() => {
      node.select();
      node.activate();
    });

    render(
      // Simulate react-arborist's default row click handler, which also
      // activates the node if the custom node click bubbles to the row.
      <div onClick={defaultRowClick}>
        <DocArboristNode
          node={node as never}
          tree={{} as never}
          style={{}}
          dragHandle={null as never}
          preview={null as never}
          onContextAction={vi.fn()}
          canWrite={false}
        />
      </div>
    );

    await user.click(screen.getByText('deploy'));

    expect(node.activate).toHaveBeenCalledTimes(1);
  });

  it('opens rename with the node workspace and visible document path', async () => {
    const user = userEvent.setup();
    const node = makeFileNode('ops');
    const onContextAction = vi.fn();

    render(
      <DocArboristNode
        node={node as never}
        tree={{} as never}
        style={{}}
        dragHandle={null as never}
        preview={null as never}
        onContextAction={onContextAction}
        canWrite
      />
    );

    await user.click(screen.getByRole('button'));
    await user.click(screen.getByText('Rename'));

    expect(onContextAction).toHaveBeenCalledWith({
      type: 'rename',
      docPath: 'runbooks/deploy',
      title: 'deploy',
      workspace: 'ops',
    });
    expect(node.edit).not.toHaveBeenCalled();
  });
});

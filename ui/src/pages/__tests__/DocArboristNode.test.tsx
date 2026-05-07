// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import * as React from 'react';
import { describe, expect, it, vi } from 'vitest';
import { DocTreeNodeResponseType } from '@/api/v1/schema';
import DocArboristNode from '../docs/components/DocArboristNode';

function makeFileNode() {
  const select = vi.fn();
  const activate = vi.fn();
  return {
    id: 'runbooks/deploy',
    data: {
      id: 'runbooks/deploy',
      name: 'deploy',
      title: 'deploy',
      type: DocTreeNodeResponseType.file,
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
    handleClick: vi.fn(() => {
      select();
      activate();
    }),
  };
}

describe('DocArboristNode', () => {
  it('activates a file once when the filename is clicked', async () => {
    const user = userEvent.setup();
    const node = makeFileNode();

    render(
      <div onClick={node.handleClick}>
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
});

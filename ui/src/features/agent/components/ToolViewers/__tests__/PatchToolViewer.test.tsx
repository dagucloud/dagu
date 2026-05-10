// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { PatchToolViewer } from '../PatchToolViewer';

function renderPatch(args: Record<string, unknown>) {
  return render(
    <UserPreferencesProvider>
      <PatchToolViewer args={args} toolName="patch" />
    </UserPreferencesProvider>
  );
}

describe('PatchToolViewer', () => {
  it('renders insert previews by operation when unused replace fields are empty', () => {
    renderPatch({
      path: '/tmp/MEMORY.md',
      operation: 'insert_after',
      anchor: '- existing\n',
      content: '- inserted\n',
      old_string: '',
      new_string: '',
    });

    expect(screen.getByText('+1')).toBeInTheDocument();
    expect(screen.getByText('- existing')).toBeInTheDocument();
    expect(screen.getByText('- inserted')).toBeInTheDocument();
    expect(screen.queryByText('-0')).not.toBeInTheDocument();
  });

  it('renders create previews by operation when unused replace fields are empty', () => {
    renderPatch({
      path: '/tmp/new.md',
      operation: 'create',
      content: '# New file\n',
      old_string: '',
      new_string: '',
    });

    expect(screen.getByText('+1')).toBeInTheDocument();
    expect(screen.getByText('# New file')).toBeInTheDocument();
    expect(screen.queryByText('-0')).not.toBeInTheDocument();
  });
});

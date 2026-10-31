// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { issuesSample } from '../../testing/fixtures';
import { FieldIssues } from '../FieldIssues';

describe('FieldIssues', () => {
  it('renders nothing without issues', () => {
    const { container } = render(<FieldIssues issues={[]} />);
    expect(container).toBeEmptyDOMElement();
    render(<FieldIssues />);
    expect(screen.queryByTestId('ve-field-issues')).toBeNull();
  });

  it('lists each message and marks errors as alerts', () => {
    render(<FieldIssues issues={issuesSample} />);
    expect(screen.getAllByRole('listitem')).toHaveLength(3);
    const alerts = screen.getAllByRole('alert');
    expect(alerts).toHaveLength(1);
    expect(alerts[0]).toHaveTextContent('unknown dependency');
    expect(
      screen.getByText('the DAG has no description').closest('li')
    ).toHaveClass('text-warning');
  });
});

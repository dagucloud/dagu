// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { ConditionMarks } from '../nodes/ConditionMarks';

describe('ConditionMarks', () => {
  it('marks the preconditions and the continue_on of a step, with their text as the tooltip', () => {
    render(
      <ConditionMarks
        node={{
          preconditions: ['test -f ready'],
          continueOn: { failure: true, markSuccess: true },
        }}
      />
    );
    expect(screen.getByTestId('ve-node-preconditions')).toHaveAttribute(
      'title',
      'Runs only if:\n• test -f ready'
    );
    expect(screen.getByTestId('ve-node-continue-on')).toHaveAttribute(
      'title',
      'continues on failure\nThe run goes on with the steps after this one.\nThe step then counts as a success.'
    );
  });

  it('shows nothing for a step that has neither key', () => {
    const { container } = render(<ConditionMarks node={{}} />);
    expect(container).toBeEmptyDOMElement();
  });
});

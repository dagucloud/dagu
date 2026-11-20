// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, screen, within } from '@testing-library/react';

/** The names of the options of a picker: it opens the list, reads it, and shuts it. */
export function optionNames(trigger: HTMLElement): string[] {
  fireEvent.click(trigger);
  const names = within(screen.getByRole('listbox'))
    .getAllByRole('option')
    .map((option) => option.textContent ?? '');
  fireEvent.keyDown(screen.getByRole('listbox'), { key: 'Escape' });
  return names;
}

/** Opens a picker and clicks the option with this text. */
export function pickOption(trigger: HTMLElement, text: string | RegExp): void {
  fireEvent.click(trigger);
  const options = within(screen.getByRole('listbox')).getAllByRole('option');
  const option = options.find((item) =>
    typeof text === 'string'
      ? item.textContent === text
      : text.test(item.textContent ?? '')
  );
  if (!option) throw new Error(`no option "${String(text)}"`);
  fireEvent.click(option);
}

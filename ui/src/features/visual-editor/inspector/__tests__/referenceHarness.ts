// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, screen } from '@testing-library/react';
import type { ReferenceCandidate } from '../../contracts';

export const candidatesSample: ReferenceCandidate[] = [
  {
    token: '${params.API_URL}',
    label: 'API_URL',
    group: 'params',
    known: 'static',
  },
];

/** Focuses a field and types "${", which opens the list of a reference field. */
export function typeOpenReference(field: HTMLElement): void {
  act(() => field.focus());
  fireEvent.change(field, { target: { value: '${' } });
}

/** The open list of references, or null. */
export function referenceList(): HTMLElement | null {
  return screen.queryByRole('listbox', { name: 'References' });
}

// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it } from 'vitest';
import {
  BackfillOnFailure,
  BackfillRerun,
  BackfillStatus,
} from '@/api/v1/schema';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { I18nProvider } from '@/i18n/I18nProvider';
import type { Backfill } from '../../lib/backfillState';
import { BackfillSentence } from '../BackfillSentence';

const running: Backfill = {
  id: 'bf123456',
  dag: 'etl',
  status: BackfillStatus.running,
  submittedAt: '2026-11-01T00:00:00Z',
  rerun: BackfillRerun.none,
  maxConcurrency: 1,
  onFailure: BackfillOnFailure.stop,
  slotCount: 28,
  counts: { succeeded: 11, failed: 1, running: 1, pending: 15 },
};

function renderSentence(backfill: Backfill) {
  render(
    <MemoryRouter>
      <BackfillSentence backfill={backfill} remoteNode="edge" />
    </MemoryRouter>
  );
}

describe('BackfillSentence', () => {
  // A running backfill tells about how long its slots left take.
  it('tells the time left', () => {
    renderSentence({ ...running, secondsLeft: 6000 });
    expect(screen.getByText(/about 1h 40m left/)).toBeInTheDocument();
  });

  // A blocked backfill links to the run it waits for.
  it('links to the run that blocks it', () => {
    renderSentence({
      ...running,
      waiting:
        'Blocked: the run of slot 2026-10-05 02:00 is waiting for a person',
      waitingRunId: 'backfill-bf123456-0-20261005T020000Z',
    });
    expect(
      screen.getByRole('link', { name: /waiting for a person/ })
    ).toHaveAttribute(
      'href',
      '/dag-runs/etl/backfill-bf123456-0-20261005T020000Z?remoteNode=edge'
    );
  });

  describe('in French', () => {
    afterEach(() => {
      localStorage.clear();
    });

    // The text of each line, with its no-break spaces.
    function renderFrench(backfill: Backfill): string[] {
      localStorage.setItem(
        'user_preferences',
        JSON.stringify({ locale: 'fr' })
      );
      const { container } = render(
        <UserPreferencesProvider>
          <I18nProvider>
            <MemoryRouter>
              <BackfillSentence backfill={backfill} remoteNode="edge" />
            </MemoryRouter>
          </I18nProvider>
        </UserPreferencesProvider>
      );
      return [...container.querySelectorAll('p')].map(
        (line) => line.textContent ?? ''
      );
    }

    it('tells where a running backfill stands', () => {
      expect(renderFrench(running)).toEqual([
        'En cours – créneaux terminés\u00a0: 12 sur 28, en échec\u00a0: 1',
      ]);
    });

    it('tells why a backfill waits for a scheduler', () => {
      expect(
        renderFrench({
          ...running,
          status: BackfillStatus.waiting_for_scheduler,
          counts: { succeeded: 2, pending: 26 },
        })
      ).toEqual([
        'En attente d’un planificateur – créneaux terminés\u00a0: 2 sur 28',
        'Aucun planificateur n’est actif, le rattrapage ne met donc aucune exécution en file d’attente.',
      ]);
    });
  });
});

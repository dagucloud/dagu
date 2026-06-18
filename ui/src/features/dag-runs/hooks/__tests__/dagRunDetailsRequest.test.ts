// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  matchesRequestedDAGRunDetails,
  type DAGRunDetails,
} from '../dagRunDetailsRequest';

function buildDetails(
  dagRunId: string,
  name: string = 'billing'
): DAGRunDetails {
  return {
    dagRunId,
    name,
  } as DAGRunDetails;
}

describe('matchesRequestedDAGRunDetails', () => {
  it('treats latest as a wildcard when details are present', () => {
    expect(
      matchesRequestedDAGRunDetails(buildDetails('resolved-run-id'), 'latest')
    ).toBe(true);
  });

  it('matches an exact dag-run id', () => {
    expect(matchesRequestedDAGRunDetails(buildDetails('run-1'), 'run-1')).toBe(
      true
    );
  });

  it('rejects a mismatched dag-run id', () => {
    expect(matchesRequestedDAGRunDetails(buildDetails('run-1'), 'run-2')).toBe(
      false
    );
  });

  it('rejects latest details for a different DAG name', () => {
    expect(
      matchesRequestedDAGRunDetails(
        buildDetails('resolved-run-id', 'billing'),
        'latest',
        'deploy'
      )
    ).toBe(false);
  });

  it('rejects missing details even for latest', () => {
    expect(matchesRequestedDAGRunDetails(null, 'latest')).toBe(false);
  });
});

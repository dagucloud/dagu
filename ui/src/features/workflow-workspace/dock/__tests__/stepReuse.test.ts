// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { NodeStatus, ReusedInputVia } from '@/api/v1/schema';
import { node, run } from '../../data/__tests__/fixtures';
import { stepReuse } from '../stepReuse';

const gone = { startedAt: '-', finishedAt: '-', doneCount: 0 };

describe('stepReuse', () => {
  // fetch -> extract -> load: the run executed load. fetch was carried from
  // the source run, extract was not: it failed there.
  const chain = () =>
    run(
      'run-1',
      [
        node('fetch', NodeStatus.Skipped),
        node('extract', NodeStatus.Skipped, {
          ...gone,
          step: { depends: ['fetch'] },
        }),
        node('load', NodeStatus.Success, { step: { depends: ['extract'] } }),
      ],
      undefined,
      {
        labels: ['dagu.outputs-from=r0abcdef12'],
        reusedInputs: [
          {
            step: 'fetch',
            runId: 'r0abcdef12',
            startedAt: '2026-01-01T00:00:00Z',
            finishedAt: '2026-01-01T00:00:05Z',
            via: ReusedInputVia.run,
          },
        ],
      }
    );

  it('marks a carried step green and a previous step that the source could not give red', () => {
    const reuse = stepReuse(chain());
    expect(reuse.get('fetch')).toEqual({
      outcome: 'success',
      runId: 'r0abcdef12',
    });
    expect(reuse.get('extract')).toEqual({
      outcome: 'failed',
      runId: 'r0abcdef12',
    });
    expect(reuse.has('load')).toBe(false);
  });

  it('leaves a skipped step on another branch alone', () => {
    const details = chain();
    details.nodes.push(node('other', NodeStatus.Skipped, gone));
    expect(stepReuse(details).has('other')).toBe(false);
  });

  it('lists nothing for a run that reused nothing', () => {
    expect(
      stepReuse(run('run-1', [node('a'), node('b', NodeStatus.Skipped, gone)]))
        .size
    ).toBe(0);
  });
});

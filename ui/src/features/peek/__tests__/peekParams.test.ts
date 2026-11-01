// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  buildDAGPageURL,
  buildDAGRunPageURL,
} from '@/features/dag-runs/lib/dagRunUrls';
import { peekPageURL, readPeek, withoutPeek, withPeek } from '../peekParams';

describe('readPeek', () => {
  it('reads a workflow, a run and a dock tab', () => {
    expect(readPeek(new URLSearchParams('peek=etl.yaml'))).toEqual({
      dag: 'etl.yaml',
    });
    expect(
      readPeek(new URLSearchParams('peek=etl&peekRun=run-1&peekDock=artifacts'))
    ).toEqual({ dag: 'etl', run: 'run-1', dock: 'artifacts' });
  });

  it('has no peek without ?peek', () => {
    expect(readPeek(new URLSearchParams('peekRun=run-1'))).toBeNull();
    expect(readPeek(new URLSearchParams('peek='))).toBeNull();
  });

  it.each([
    ['a name with a path', 'peek=..%2Fsecrets'],
    ['the current directory', 'peek=.'],
    ['the parent directory', 'peek=..'],
    ['a name with a space', 'peek=my+dag'],
    ['a run ID with a colon', 'peek=etl&peekRun=run%3A1'],
    ['an unknown dock form', 'peek=etl&peekDock=Log%20tab'],
  ])('refuses %s', (_, search) => {
    expect(readPeek(new URLSearchParams(search))).toBeNull();
  });
});

describe('withPeek and withoutPeek', () => {
  it('adds and drops the peek keys and keeps the list keys', () => {
    const opened = withPeek('?view=all&sort=name', {
      dag: 'etl',
      run: 'run-1',
    });
    expect(opened).toBe('?view=all&sort=name&peek=etl&peekRun=run-1');
    expect(withoutPeek(opened)).toBe('?view=all&sort=name');
  });

  it('replaces the keys of a peek that is already open', () => {
    expect(
      withPeek('?peek=etl&peekRun=run-1&peekDock=artifacts', { dag: 'etl' })
    ).toBe('?peek=etl');
  });

  it('leaves an empty search empty', () => {
    expect(withoutPeek('?peek=etl')).toBe('');
    expect(withoutPeek('')).toBe('');
  });
});

// The Open button, a Ctrl click and the narrow-screen path all use
// peekPageURL: it must be the URL that each kind of row links.
describe('peekPageURL', () => {
  it('is the link of a workflow row', () => {
    expect(peekPageURL({ dag: 'etl.yaml' }, 'local')).toBe(
      buildDAGPageURL({ fileName: 'etl.yaml', remoteNode: 'local' })
    );
  });

  it('is the link of a run row', () => {
    expect(peekPageURL({ dag: 'etl', run: 'run-1' }, 'remote-a')).toBe(
      buildDAGRunPageURL({
        rootDAGRunName: 'etl',
        rootDAGRunId: 'run-1',
        remoteNode: 'remote-a',
      })
    );
  });

  it('is the link of a run card with artifacts', () => {
    expect(
      peekPageURL({ dag: 'etl', run: 'run-1', dock: 'artifacts' }, 'local')
    ).toBe('/dags/etl?remoteNode=local&run=run-1&dock=artifacts');
  });

  it('opens a step of the workflow or of the run', () => {
    expect(peekPageURL({ dag: 'etl' }, 'local', 'load')).toBe(
      '/dags/etl?remoteNode=local&step=load'
    );
    expect(peekPageURL({ dag: 'etl', run: 'run-1' }, 'local', 'load')).toBe(
      '/dags/etl?remoteNode=local&run=run-1&step=load'
    );
  });
});

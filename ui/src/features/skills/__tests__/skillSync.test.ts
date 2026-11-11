// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  attachmentName,
  classifyFailure,
  downloadSkillZip,
  INITIAL_SYNC,
  removeSkillFile,
  syncReducer,
  writeHash,
  writeSkillFile,
  writeSkillText,
  type SyncAction,
  type SyncState,
} from '../skillSync';

const saved = vi.hoisted(() => [] as Array<{ blob: unknown; name: string }>);
vi.mock('@/lib/download', () => ({
  downloadBlob: (blob: unknown, name: string) => saved.push({ blob, name }),
}));

function run(actions: SyncAction[], from: SyncState = INITIAL_SYNC): SyncState {
  return actions.reduce(syncReducer, from);
}

describe('hash chain', () => {
  it('sends the loaded hash until a write gives a new one', () => {
    expect(writeHash(INITIAL_SYNC, 'h1')).toBe('h1');
    expect(writeHash(run([{ type: 'wrote', hash: 'h2' }]), 'h1')).toBe('h2');
    expect(writeHash(INITIAL_SYNC, undefined)).toBe('');
  });

  it('chains each write on the hash of the one before', () => {
    const state = run([
      { type: 'wrote', hash: 'h2' },
      { type: 'wrote', hash: 'h3' },
    ]);
    expect(state).toEqual({ base: 'h3', conflict: null });
  });

  it('keeps the hash and remembers the server hash on a conflict', () => {
    const state = run([
      { type: 'wrote', hash: 'h2' },
      { type: 'conflict', hash: 'h9' },
    ]);
    expect(state).toEqual({ base: 'h2', conflict: { hash: 'h9' } });
  });

  it('takes the server hash when the user keeps the draft', () => {
    const state = run([{ type: 'conflict', hash: 'h9' }, { type: 'rebase' }]);
    expect(state).toEqual({ base: 'h9', conflict: null });
  });

  it('cannot rebase onto a skill that no longer exists', () => {
    const gone = run([{ type: 'conflict', hash: '' }]);
    expect(run([{ type: 'rebase' }], gone)).toEqual(gone);
    expect(run([{ type: 'rebase' }])).toEqual(INITIAL_SYNC);
  });

  it('clears everything on a reload, and a write clears a conflict', () => {
    expect(run([{ type: 'conflict', hash: 'h9' }, { type: 'reset' }])).toEqual(
      INITIAL_SYNC
    );
    expect(
      run([
        { type: 'conflict', hash: 'h9' },
        { type: 'wrote', hash: 'h5' },
      ])
    ).toEqual({ base: 'h5', conflict: null });
  });
});

describe('classifyFailure', () => {
  it('maps a status to the kind of failure', () => {
    expect(
      classifyFailure(409, { hash: 'abc', message: 'changed' }, 'x')
    ).toEqual({ kind: 'conflict', hash: 'abc', message: 'changed' });
    expect(classifyFailure(409, undefined, 'x')).toEqual({
      kind: 'conflict',
      hash: '',
      message: 'x',
    });
    expect(classifyFailure(400, { message: 'bad', issues: [] }, 'x')).toEqual({
      kind: 'invalid',
      issues: [],
      message: 'bad',
    });
    expect(classifyFailure(400, undefined, 'x')).toMatchObject({
      kind: 'invalid',
      issues: [],
    });
    expect(classifyFailure(413, { message: 'big' }, 'x').kind).toBe(
      'too-large'
    );
    expect(classifyFailure(403, undefined, 'x').kind).toBe('forbidden');
    expect(classifyFailure(404, undefined, 'x').kind).toBe('missing');
    expect(classifyFailure(428, undefined, 'x')).toEqual({
      kind: 'other',
      message: 'x',
    });
  });
});

describe('attachmentName', () => {
  it('reads the quoted and the bare filename', () => {
    expect(
      attachmentName('attachment; filename="skills-2026-11-05.zip"', 'f.zip')
    ).toBe('skills-2026-11-05.zip');
    expect(attachmentName('attachment; filename=triage.zip', 'f.zip')).toBe(
      'triage.zip'
    );
  });

  it('prefers filename* and decodes it', () => {
    expect(
      attachmentName(
        'attachment; filename="fallback.zip"; filename*=UTF-8\'\'sk%C3%AEll.zip',
        'f.zip'
      )
    ).toBe('skîll.zip');
  });

  it('drops a path, and falls back when there is no name', () => {
    expect(
      attachmentName('attachment; filename="../../etc/x.zip"', 'f.zip')
    ).toBe('x.zip');
    expect(attachmentName('attachment', 'f.zip')).toBe('f.zip');
    expect(attachmentName(null, 'f.zip')).toBe('f.zip');
    expect(attachmentName('attachment; filename=""', 'f.zip')).toBe('f.zip');
  });
});

describe('requests', () => {
  const client = {
    PUT: vi.fn(),
    DELETE: vi.fn(),
    POST: vi.fn(),
  };
  const target = { name: 'triage', remoteNode: 'edge' };
  const ok = {
    data: { hash: 'h2', errors: [], warnings: [], files: [] },
    response: { ok: true, status: 200 },
  };

  beforeEach(() => {
    saved.length = 0;
    client.PUT.mockReset().mockResolvedValue(ok);
    client.DELETE.mockReset().mockResolvedValue(ok);
    client.POST.mockReset();
  });

  it('sends If-Match to update SKILL.md and If-None-Match to create it', async () => {
    await writeSkillText(client as never, target, 'text', { hash: 'h1' }, 'f');
    await writeSkillText(client as never, target, 'text', 'create', 'f');
    expect(client.PUT.mock.calls[0]![1].params.header).toEqual({
      'If-Match': 'h1',
    });
    expect(client.PUT.mock.calls[1]![1].params.header).toEqual({
      'If-None-Match': '*',
    });
    expect(client.PUT.mock.calls[0]![1].body).toEqual({ content: 'text' });
  });

  it('writes a file as raw bytes with If-Match and the path in the query', async () => {
    const outcome = await writeSkillFile(
      client as never,
      target,
      'references/a.md',
      'hello',
      'h1',
      'f'
    );
    expect(outcome).toEqual({ ok: true, result: ok.data });
    const init = client.PUT.mock.calls[0]![1];
    expect(client.PUT.mock.calls[0]![0]).toBe('/skills/{name}/files');
    expect(init.params.query).toEqual({
      remoteNode: 'edge',
      path: 'references/a.md',
    });
    expect(init.params.header).toEqual({ 'If-Match': 'h1' });
    expect(init.headers).toEqual({
      'Content-Type': 'application/octet-stream',
    });
    // The body goes out as it is, not as JSON.
    expect(init.bodySerializer('hello')).toBe('hello');
  });

  it('returns the conflict with the server hash, and the 400 issues', async () => {
    client.PUT.mockResolvedValueOnce({
      error: { message: 'changed', hash: 'h9' },
      response: { ok: false, status: 409 },
    });
    expect(
      await writeSkillFile(client as never, target, 'assets/a', 'x', 'h1', 'f')
    ).toEqual({ ok: false, kind: 'conflict', hash: 'h9', message: 'changed' });
    client.PUT.mockResolvedValueOnce({
      error: { message: 'bad', issues: [{ rule: 'r' }] },
      response: { ok: false, status: 400 },
    });
    expect(
      await writeSkillText(client as never, target, 'x', { hash: 'h1' }, 'f')
    ).toMatchObject({ ok: false, kind: 'invalid', issues: [{ rule: 'r' }] });
  });

  it('turns a network failure into a failure with its message', async () => {
    client.PUT.mockRejectedValueOnce(new Error('offline'));
    expect(
      await writeSkillText(client as never, target, 'x', { hash: 'h1' }, 'f')
    ).toEqual({ ok: false, kind: 'other', message: 'offline' });
  });

  it('deletes a file with If-Match', async () => {
    await removeSkillFile(client as never, target, 'assets/a.png', 'h1', 'f');
    expect(client.DELETE.mock.calls[0]![0]).toBe('/skills/{name}/files');
    expect(client.DELETE.mock.calls[0]![1].params.header).toEqual({
      'If-Match': 'h1',
    });
  });

  it('downloads the ZIP and saves it under the name of the header', async () => {
    client.POST.mockResolvedValue({
      data: new Blob(['zip']),
      response: {
        ok: true,
        headers: new Headers({
          'Content-Disposition': 'attachment; filename="triage.zip"',
        }),
      },
    });
    const name = await downloadSkillZip(client as never, 'edge', ['triage']);
    expect(name).toBe('triage.zip');
    expect(saved).toHaveLength(1);
    expect(client.POST.mock.calls[0]![1].body).toEqual({ names: ['triage'] });
  });

  it('uses a default name when the header is absent, and throws on an error', async () => {
    client.POST.mockResolvedValueOnce({
      data: new Blob(['zip']),
      response: { ok: true, headers: new Headers() },
    });
    expect(await downloadSkillZip(client as never, 'edge', ['a', 'b'])).toBe(
      'skills.zip'
    );
    client.POST.mockResolvedValueOnce({
      error: { message: 'too large' },
      response: {
        ok: false,
        statusText: 'Bad Request',
        headers: new Headers(),
      },
    });
    await expect(
      downloadSkillZip(client as never, 'edge', ['a'])
    ).rejects.toThrow('too large');
  });
});

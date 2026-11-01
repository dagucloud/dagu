// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, render } from '@testing-library/react';
import { useEffect } from 'react';
import {
  MemoryRouter,
  Route,
  Routes,
  useLocation,
  useNavigate,
  useNavigationType,
} from 'react-router-dom';
import { describe, expect, it } from 'vitest';

import {
  buildWorkspaceUrl,
  DOCK_TABS,
  parseWorkspaceUrl,
  useWorkspaceUrl,
  type SetWorkspaceUrl,
  type WorkspaceUrlState,
} from '../workspaceUrl';

const BASE: WorkspaceUrlState = {
  mode: 'workspace',
  popout: null,
  run: null,
  subRun: null,
  def: null,
  step: null,
  ndv: null,
  dock: null,
  stream: null,
  item: null,
  body: null,
  sub: null,
  panel: null,
  wtab: null,
  carry: {},
};

function parse(tab: string | undefined, query = ''): WorkspaceUrlState {
  return parseWorkspaceUrl(tab, new URLSearchParams(query));
}

/** Splits a built URL into the route param :tab and the query, as the router does. */
function parseBuilt(url: string): WorkspaceUrlState {
  const [path = '', query = ''] = url.split('?');
  const segments = path.split('/');
  expect(segments[1]).toBe('dags');
  const tab =
    segments[3] === undefined ? undefined : decodeURIComponent(segments[3]);
  return parseWorkspaceUrl(tab, new URLSearchParams(query));
}

describe('parseWorkspaceUrl', () => {
  const rows: Array<[string | undefined, string, Partial<WorkspaceUrlState>]> =
    [
      [undefined, '', {}],
      // The old Executions tab reads as the workspace; the redirect drops it.
      ['executions', '', {}],
      // The old section routes are legacy links: the editor path.
      ['settings', '', {}],
      ['webhook', '', {}],
      ['wiki', '', {}],
      [
        undefined,
        'panel=workflow&wtab=wiki',
        { panel: 'workflow', wtab: 'wiki' },
      ],
      [undefined, 'panel=webhook', { panel: 'webhook' }],
      [undefined, 'panel=nope&wtab=nope', {}],
      // The Profile tab is gone: its link opens the Overview.
      [undefined, 'panel=workflow&wtab=profile', { panel: 'workflow' }],
      [
        'log',
        'dagRunId=r1&dagRunName=root&subDAGRunId=s1&step=build&stream=stderr',
        {
          mode: 'popout',
          popout: {
            kind: 'step',
            dagRunId: 'r1',
            dagRunName: 'root',
            subDAGRunId: 's1',
          },
          step: 'build',
          stream: 'stderr',
        },
      ],
      [
        'dagRun-log',
        'dagRunId=r2',
        { mode: 'popout', popout: { kind: 'run', dagRunId: 'r2' } },
      ],
      ['dagRun-log', '', { mode: 'popout', popout: { kind: 'run' } }],
      ['executions', 'run=r9', { run: 'r9' }],
      [undefined, 'def=run', { def: 'run' }],
      [undefined, 'run=r4&def=run', { run: 'r4', def: 'run' }],
      [undefined, 'def=live', {}],
      [undefined, 'step=fetch', { step: 'fetch' }],
      [undefined, 'ndv=docked', { ndv: 'docked' }],
      [undefined, 'ndv=expanded', { ndv: 'expanded' }],
      [undefined, 'ndv=floating', {}],
      [undefined, 'dock=timeline', { dock: 'timeline' }],
      [undefined, 'dock=nope', {}],
      [undefined, 'stream=stdout', { stream: 'stdout' }],
      [undefined, 'stream=other', {}],
      [undefined, 'item=a%2Fb', { item: 'a/b' }],
      [undefined, 'body=inner', { body: 'inner' }],
      [undefined, 'sub=child-1', { sub: 'child-1' }],
      // The step of a sub-run link is a step of that sub-run, not of the run.
      [
        undefined,
        'run=r1&subRun=c1&step=deploy',
        { run: 'r1', subRun: { id: 'c1', step: 'deploy' } },
      ],
      [undefined, 'subRun=c1', { subRun: { id: 'c1', step: null } }],
      // The List and Grid views are gone: their ?view is ignored.
      ['executions', 'view=grid', {}],
      // The Steps and Spec tabs are gone: their links show the default tab.
      [undefined, 'dock=steps', {}],
      [undefined, 'dock=spec', {}],
      [undefined, 'run=', {}],
      [
        undefined,
        'remoteNode=edge&workspace=ops',
        { carry: { remoteNode: 'edge', workspace: 'ops' } },
      ],
      // The ?ui switch is gone: the page ignores it.
      [undefined, 'ui=classic', {}],
      [undefined, 'ui=workspace', {}],
      // Unknown tabs show the editor; the legacy redirect sends them elsewhere.
      ['status', '', {}],
      // Names of Object.prototype members are not sections.
      ['constructor', '', {}],
      ['toString', '', {}],
      ['__proto__', '', {}],
    ];

  it.each(rows)('tab %s with ?%s', (tab, query, expected) => {
    expect(parse(tab, query)).toStrictEqual({ ...BASE, ...expected });
  });

  // The run page keeps ?tab; ?dock is its twin in the workspace.
  // parity:run-page-tab-deeplink
  it('accepts every dock tab', () => {
    for (const tab of DOCK_TABS) {
      expect(parse(undefined, `dock=${tab}`).dock).toBe(tab);
      expect(buildWorkspaceUrl('x', { ...BASE, dock: tab })).toBe(
        `/dags/x?dock=${tab}`
      );
    }
  });
});

describe('buildWorkspaceUrl', () => {
  it('writes the path of each mode', () => {
    expect(buildWorkspaceUrl('x', BASE)).toBe('/dags/x');
    expect(
      buildWorkspaceUrl('x', { ...BASE, panel: 'workflow', wtab: 'wiki' })
    ).toBe('/dags/x?panel=workflow&wtab=wiki');
    expect(
      buildWorkspaceUrl('x', {
        ...BASE,
        mode: 'popout',
        popout: { kind: 'run', dagRunId: 'r1' },
      })
    ).toBe('/dags/x/dagRun-log?dagRunId=r1');
    expect(
      buildWorkspaceUrl('x', {
        ...BASE,
        mode: 'popout',
        popout: { kind: 'step', dagRunId: 'r1' },
        step: 's',
      })
    ).toBe('/dags/x/log?dagRunId=r1&step=s');
  });

  it('writes every query key', () => {
    const url = buildWorkspaceUrl('x', {
      ...BASE,
      run: 'r',
      step: 's',
      ndv: 'docked',
      dock: 'log',
      stream: 'stderr',
      item: 'i',
      body: 'b',
      sub: 'c',
      carry: { remoteNode: 'n', workspace: 'w' },
    });
    expect(url).toBe(
      '/dags/x?run=r&step=s&ndv=docked&dock=log&stream=stderr&item=i&body=b&sub=c&remoteNode=n&workspace=w'
    );
  });

  it('writes only the carry that is present', () => {
    expect(
      buildWorkspaceUrl('x', { ...BASE, carry: { remoteNode: 'local' } })
    ).toBe('/dags/x?remoteNode=local');
  });
});

describe('round trip', () => {
  // Deterministic generator (mulberry32), so a failure reproduces.
  function rng(seed: number) {
    let a = seed;
    return () => {
      a = (a + 0x6d2b79f5) | 0;
      let t = Math.imul(a ^ (a >>> 15), 1 | a);
      t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
      return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
    };
  }

  function generate(seed: number): WorkspaceUrlState {
    const r = rng(seed);
    const pick = <T,>(values: readonly T[]): T =>
      values[Math.floor(r() * values.length)] as T;
    const maybe = (value: string): string | null =>
      r() < 0.5 ? null : `${value}-${Math.floor(r() * 100)}`;

    const mode = pick(['workspace', 'popout'] as const);
    const state: WorkspaceUrlState = {
      ...BASE,
      mode,
      panel: pick([null, 'workflow', 'cron', 'webhook'] as const),
      wtab: pick([
        null,
        'overview',
        'wiki',
        'incidents',
        'notifications',
      ] as const),
      popout: null,
      run: maybe('run'),
      step: maybe('step'),
      ndv: pick([null, 'docked', 'expanded'] as const),
      dock: pick([null, ...DOCK_TABS]),
      stream: pick([null, 'stdout', 'stderr'] as const),
      item: maybe('item/x'),
      body: maybe('body'),
      sub: maybe('sub'),
      carry: {},
    };
    if (mode === 'popout') {
      const popout: NonNullable<WorkspaceUrlState['popout']> = {
        kind: pick(['step', 'run'] as const),
      };
      const dagRunId = maybe('id');
      if (dagRunId) popout.dagRunId = dagRunId;
      const dagRunName = maybe('name');
      if (dagRunName) popout.dagRunName = dagRunName;
      const subDAGRunId = maybe('sub');
      if (subDAGRunId) popout.subDAGRunId = subDAGRunId;
      state.popout = popout;
    }
    const remoteNode = maybe('node');
    if (remoteNode) state.carry.remoteNode = remoteNode;
    const workspace = maybe('ws');
    if (workspace) state.carry.workspace = workspace;
    return state;
  }

  it.each(Array.from({ length: 20 }, (_, i) => i + 1))(
    'parse(build(s)) equals s for generated state %i',
    (seed) => {
      const state = generate(seed);
      expect(parseBuilt(buildWorkspaceUrl('my dag', state))).toStrictEqual(
        state
      );
    }
  );

  // The pop-out pages read the run, the step and the stream from these keys.
  it.each([
    [
      'a step log of a root run',
      {
        ...BASE,
        mode: 'popout',
        popout: { kind: 'step', dagRunId: 'r1' },
        step: 'build',
        stream: 'stderr',
      },
      '/dags/my%20dag/log?dagRunId=r1&step=build&stream=stderr',
    ],
    [
      'a step log of a sub-run',
      {
        ...BASE,
        mode: 'popout',
        popout: {
          kind: 'step',
          dagRunId: 'r1',
          dagRunName: 'root',
          subDAGRunId: 's1',
        },
        step: 'compile',
        carry: { remoteNode: 'edge', workspace: 'ops' },
      },
      '/dags/my%20dag/log?dagRunId=r1&dagRunName=root&subDAGRunId=s1&step=compile&remoteNode=edge&workspace=ops',
    ],
    [
      'the run log of a sub-run',
      {
        ...BASE,
        mode: 'popout',
        popout: {
          kind: 'run',
          dagRunId: 'r1',
          dagRunName: 'root',
          subDAGRunId: 's1',
        },
      },
      '/dags/my%20dag/dagRun-log?dagRunId=r1&dagRunName=root&subDAGRunId=s1',
    ],
    [
      'the run log of the latest run',
      { ...BASE, mode: 'popout', popout: { kind: 'run' } },
      '/dags/my%20dag/dagRun-log',
    ],
    [
      'a sub-run link with its step',
      { ...BASE, run: 'r1', subRun: { id: 'c1', step: 'deploy' } },
      '/dags/my%20dag?run=r1&subRun=c1&step=deploy',
    ],
  ] as Array<[string, WorkspaceUrlState, string]>)(
    'round-trips %s',
    (_, state, url) => {
      expect(buildWorkspaceUrl('my dag', state)).toBe(url);
      expect(parseBuilt(url)).toStrictEqual(state);
    }
  );

  it('carries remoteNode and workspace only when present', () => {
    const both = parseBuilt(
      buildWorkspaceUrl('x', {
        ...BASE,
        carry: { remoteNode: 'n', workspace: 'w' },
      })
    );
    expect(both.carry).toStrictEqual({ remoteNode: 'n', workspace: 'w' });
    const one = parseBuilt(
      buildWorkspaceUrl('x', { ...BASE, carry: { remoteNode: 'n' } })
    );
    expect(one.carry).toStrictEqual({ remoteNode: 'n' });
  });
});

type Entry = { url: string; action: string };

function Probe({
  onReady,
  log,
}: {
  onReady(state: WorkspaceUrlState, set: SetWorkspaceUrl): void;
  log: Entry[];
}) {
  const [state, set] = useWorkspaceUrl();
  const location = useLocation();
  const action = useNavigationType();
  useEffect(() => {
    log.push({ url: `${location.pathname}${location.search}`, action });
  }, [action, location, log]);
  onReady(state, set);
  return null;
}

function renderHook(initial: string) {
  const log: Entry[] = [];
  const current: { state?: WorkspaceUrlState; set?: SetWorkspaceUrl } = {};
  render(
    <MemoryRouter initialEntries={[initial]}>
      <Routes>
        {['/dags/:fileName', '/dags/:fileName/:tab'].map((path) => (
          <Route
            key={path}
            path={path}
            element={
              <Probe
                log={log}
                onReady={(state, set) => {
                  current.state = state;
                  current.set = set;
                }}
              />
            }
          />
        ))}
      </Routes>
    </MemoryRouter>
  );
  const update = (...args: Parameters<SetWorkspaceUrl>) =>
    act(() => {
      current.set!(...args);
    });
  const last = () => log[log.length - 1]!;
  return { log, current, update, last };
}

describe('useWorkspaceUrl', () => {
  it('reads the route and the query', () => {
    const { current } = renderHook('/dags/x/executions?run=r1&dock=steps');
    expect(current.state).toStrictEqual({
      ...BASE,
      run: 'r1',
    });
  });

  it.each([
    [{ mode: 'popout' as const }, '/dags/x/log'],
    [{ run: 'r2' }, '/dags/x?run=r2'],
    [{ def: 'run' as const }, '/dags/x?def=run'],
  ])('pushes for %o', (patch, url) => {
    const { update, last } = renderHook('/dags/x');
    update(patch);
    expect(last()).toStrictEqual({ url, action: 'PUSH' });
  });

  it.each([
    [{ step: 's' }, '/dags/x?step=s'],
    [{ ndv: 'docked' as const }, '/dags/x?ndv=docked'],
    [{ dock: 'outputs' as const }, '/dags/x?dock=outputs'],
    [{ stream: 'stderr' as const }, '/dags/x?stream=stderr'],
    [{ item: 'i' }, '/dags/x?item=i'],
    [{ body: 'b' }, '/dags/x?body=b'],
    [{ sub: 'c' }, '/dags/x?sub=c'],
    [{ wtab: 'wiki' as const }, '/dags/x?wtab=wiki'],
    [{ panel: 'cron' as const }, '/dags/x?panel=cron'],
  ])('replaces for %o', (patch, url) => {
    const { update, last } = renderHook('/dags/x');
    update(patch);
    expect(last()).toStrictEqual({ url, action: 'REPLACE' });
  });

  it('replaces when asked, even for a push key', () => {
    const { update, last } = renderHook('/dags/x');
    update({ run: 'r3' }, { replace: true });
    expect(last()).toStrictEqual({ url: '/dags/x?run=r3', action: 'REPLACE' });
  });

  it('writes and clears ?def next to ?run', () => {
    const { update, last } = renderHook('/dags/x?run=r1');
    update({ def: 'run' });
    expect(last()).toStrictEqual({
      url: '/dags/x?run=r1&def=run',
      action: 'PUSH',
    });
    update({ def: null });
    expect(last()).toStrictEqual({ url: '/dags/x?run=r1', action: 'PUSH' });
  });

  it('clears a sub-run link together with its step', () => {
    const { update, last } = renderHook('/dags/x?run=r1&subRun=c1&step=s');
    update({ subRun: null });
    expect(last()).toStrictEqual({ url: '/dags/x?run=r1', action: 'REPLACE' });
  });

  // The user picked a step of the run before the sub-run opened.
  it('drops a sub-run link when a step of the run is selected', () => {
    const { update, last } = renderHook('/dags/x?run=r1&subRun=c1&step=s');
    update({ step: 'a' });
    expect(last().url).toBe('/dags/x?run=r1&step=a');
  });

  it('keeps query keys that it does not own', () => {
    const { update, last } = renderHook('/dags/x?foo=1&step=a&bar=2');
    update({ step: 'b' });
    expect(last().url).toBe('/dags/x?step=b&foo=1&bar=2');
    update({ mode: 'popout', step: null });
    expect(last().url).toBe('/dags/x/log?foo=1&bar=2');
  });

  // parity:shell-tab-change-handler
  it('keeps the remote node and the workspace when the mode changes', () => {
    const { update, last, current } = renderHook(
      '/dags/x?remoteNode=edge&workspace=ops'
    );
    update({ mode: 'popout' });
    expect(last()).toStrictEqual({
      url: '/dags/x/log?remoteNode=edge&workspace=ops',
      action: 'PUSH',
    });
    update({ run: 'r9' });
    expect(last().url).toBe('/dags/x/log?run=r9&remoteNode=edge&workspace=ops');
    expect(current.state?.carry).toStrictEqual({
      remoteNode: 'edge',
      workspace: 'ops',
    });
  });

  it('composes two updates made before the next render', () => {
    const { current, last } = renderHook('/dags/x');
    act(() => {
      current.set!({ dock: 'log' });
      current.set!({ step: 's' });
    });
    expect(last().url).toBe('/dags/x?step=s&dock=log');
  });

  it('does not navigate when the URL does not change', () => {
    const { update, log } = renderHook('/dags/x?step=s');
    const before = log.length;
    update({ step: 's' });
    expect(log.length).toBe(before);
  });

  it('does not navigate when only the query order differs', () => {
    const { update, log } = renderHook('/dags/x?step=s&run=r');
    const before = log.length;
    update({ step: 's' });
    expect(log.length).toBe(before);
  });
});

// The mark of the redirect to the latest run (router state) tells a ?run that
// the page added from a link. A write that keeps the ?run keeps the mark.
describe('useWorkspaceUrl redirect mark', () => {
  function mount(initial: {
    pathname: string;
    search: string;
    state: unknown;
  }) {
    const seen: { state?: unknown; set?: SetWorkspaceUrl } = {};
    function Reader() {
      const [, set] = useWorkspaceUrl();
      seen.state = useLocation().state;
      seen.set = set;
      return null;
    }
    render(
      <MemoryRouter initialEntries={[initial]}>
        <Routes>
          <Route path="/dags/:fileName" element={<Reader />} />
        </Routes>
      </MemoryRouter>
    );
    return seen;
  }

  it('keeps the mark when a step is selected', () => {
    const seen = mount({
      pathname: '/dags/x',
      search: '?run=r1',
      state: { wwRunRedirect: true },
    });
    act(() => seen.set!({ step: 's' }));
    expect(seen.state).toEqual({ wwRunRedirect: true });
  });

  it('drops the mark when another run is chosen', () => {
    const seen = mount({
      pathname: '/dags/x',
      search: '?run=r1',
      state: { wwRunRedirect: true },
    });
    act(() => seen.set!({ run: 'r2' }));
    expect(seen.state).toBeNull();
  });
});

describe('useWorkspaceUrl with several writers', () => {
  type Writer = { state?: WorkspaceUrlState; set?: SetWorkspaceUrl };

  function Writer({ target, log }: { target: Writer; log?: Entry[] }) {
    const [state, set] = useWorkspaceUrl();
    const location = useLocation();
    const action = useNavigationType();
    useEffect(() => {
      log?.push({ url: `${location.pathname}${location.search}`, action });
    }, [action, location, log]);
    target.state = state;
    target.set = set;
    return null;
  }

  function Two({ a, b, log }: { a: Writer; b: Writer; log?: Entry[] }) {
    return (
      <Routes>
        {['/dags/:fileName', '/dags/:fileName/:tab'].map((path) => (
          <Route
            key={path}
            path={path}
            element={
              <>
                <Writer target={a} log={log} />
                <Writer target={b} />
              </>
            }
          />
        ))}
      </Routes>
    );
  }

  function mountTwo(initial: string) {
    const a: Writer = {};
    const b: Writer = {};
    const log: Entry[] = [];
    const view = render(
      <MemoryRouter initialEntries={[initial]}>
        <Two a={a} b={b} log={log} />
      </MemoryRouter>
    );
    return { a, b, log, view, last: () => log[log.length - 1]! };
  }

  it('keeps both patches of two instances in one tick (step, ndv)', () => {
    const { a, b, last } = mountTwo('/dags/x');
    act(() => {
      a.set!({ step: 's' });
      b.set!({ ndv: 'docked' });
    });
    expect(last().url).toBe('/dags/x?step=s&ndv=docked');
    expect(a.state).toMatchObject({ step: 's', ndv: 'docked' });
    expect(b.state).toMatchObject({ step: 's', ndv: 'docked' });
  });

  it('keeps both patches of two instances in one tick (dock, run)', () => {
    const { a, b, last } = mountTwo('/dags/x?step=s');
    act(() => {
      a.set!({ dock: 'log' });
      b.set!({ run: 'r1' });
    });
    expect(last()).toStrictEqual({
      url: '/dags/x?run=r1&step=s&dock=log',
      action: 'PUSH',
    });
  });

  it('lets replace override on the second writer', () => {
    const { a, b, last } = mountTwo('/dags/x');
    act(() => {
      a.set!({ step: 's' });
      b.set!({ run: 'r2' }, { replace: true });
    });
    expect(last()).toStrictEqual({
      url: '/dags/x?run=r2&step=s',
      action: 'REPLACE',
    });
  });

  it('starts from the committed URL once the router has rendered', () => {
    const { a, b, last } = mountTwo('/dags/x');
    act(() => {
      a.set!({ step: 's' });
    });
    act(() => {
      b.set!({ ndv: 'expanded' });
    });
    expect(last().url).toBe('/dags/x?step=s&ndv=expanded');
    act(() => {
      a.set!({ step: null });
      b.set!({ ndv: null });
    });
    expect(last().url).toBe('/dags/x');
  });

  it('does not reuse a pending URL after going back to its location', () => {
    const a: Writer = {};
    const b: Writer = {};
    const log: Entry[] = [];
    const nav: { go?: (delta: number) => void } = {};
    function Nav() {
      const navigate = useNavigate();
      nav.go = (delta) => navigate(delta);
      return null;
    }
    render(
      <MemoryRouter initialEntries={['/dags/x']}>
        <Nav />
        <Two a={a} b={b} log={log} />
      </MemoryRouter>
    );
    act(() => {
      a.set!({ run: 'r1' });
      b.set!({ step: 's' });
    });
    expect(log[log.length - 1]!.url).toBe('/dags/x?run=r1&step=s');
    act(() => nav.go!(-1));
    expect(log[log.length - 1]!.url).toBe('/dags/x');
    act(() => {
      b.set!({ ndv: 'docked' });
    });
    expect(log[log.length - 1]!.url).toBe('/dags/x?ndv=docked');
  });

  // A callback of an earlier render (a frame callback) writes after a commit.
  it('starts a stale setter from the rendered location', () => {
    const { a, b, last } = mountTwo('/dags/x');
    const stale = a.set!;
    act(() => {
      b.set!({ mode: 'popout' });
    });
    expect(last().url).toBe('/dags/x/log');
    act(() => {
      stale({ step: 's' });
    });
    expect(last().url).toBe('/dags/x/log?step=s');
  });

  it('does not leak a pending URL between routers', () => {
    const first = mountTwo('/dags/x');
    act(() => {
      first.a.set!({ step: 'leak' });
    });
    first.view.unmount();
    // A new router starts on a location with the same key ('default').
    const second = mountTwo('/dags/y');
    act(() => {
      second.b.set!({ ndv: 'docked' });
    });
    expect(second.last().url).toBe('/dags/y?ndv=docked');
  });
});

describe('?view', () => {
  // A link from the List and Grid time: the next URL write drops ?view.
  it('is dropped by the next write', () => {
    const { update, last } = renderHook('/dags/x?view=grid&run=r1');
    update({ step: 's' });
    expect(last().url).toBe('/dags/x?run=r1&step=s');
  });
});

describe('?yaml', () => {
  // The legacy /spec link: the next URL write drops ?yaml.
  it('is dropped by the next write', () => {
    const { update, last } = renderHook('/dags/x?yaml=1&run=r1');
    update({ ndv: 'docked' });
    expect(last().url).toBe('/dags/x?run=r1&ndv=docked');
  });
});

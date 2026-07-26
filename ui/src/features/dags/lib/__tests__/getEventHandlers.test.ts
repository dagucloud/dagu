import { describe, expect, it } from 'vitest';
import { getEventHandlers } from '../getEventHandlers';

describe('getEventHandlers', () => {
  it('includes the onAbort lifecycle hook as-is', () => {
    const dagRun = {
      onAbort: {
        step: { name: 'onAbort' },
      },
    } as any;

    const handlers = getEventHandlers(dagRun);
    const handler = handlers[0]!;

    expect(handlers).toHaveLength(1);
    expect(handler.step.name).toBe('onAbort');
    expect(handler).toBe(dagRun.onAbort);
  });

  it('preserves non-abort handlers as-is', () => {
    const dagRun = {
      onSuccess: {
        step: { name: 'onSuccess' },
      },
      onFailure: {
        step: { name: 'onFailure' },
      },
      onExit: {
        step: { name: 'onExit' },
      },
    } as any;

    const handlers = getEventHandlers(dagRun);

    expect(handlers.map((h: any) => h.step.name)).toEqual([
      'onSuccess',
      'onFailure',
      'onExit',
    ]);
  });

  it('lists every recorded lifecycle hook in run order', () => {
    const dagRun = {
      onInit: { step: { name: 'onInit' } },
      onWait: { step: { name: 'onWait' } },
      onSuccess: { step: { name: 'onSuccess' } },
      onFailure: { step: { name: 'onFailure' } },
      onAbort: { step: { name: 'onAbort' } },
      onExit: { step: { name: 'onExit' } },
    } as any;

    const handlers = getEventHandlers(dagRun);

    expect(handlers.map((h: any) => h.step.name)).toEqual([
      'onInit',
      'onWait',
      'onSuccess',
      'onFailure',
      'onAbort',
      'onExit',
    ]);
  });
});

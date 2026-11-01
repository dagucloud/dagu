// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { yamlArraySteps } from '../testing/fixtures';
import {
  DOC_MODEL_DELAY_MS,
  FROZEN_MODEL_DELAY_MS,
  useDocModel,
} from '../useDocModel';

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
});

describe('useDocModel', () => {
  it('parses the first text at once', () => {
    const { result } = renderHook(() => useDocModel(yamlArraySteps));

    expect(result.current.frozen).toBe(false);
    expect(result.current.model.text).toBe(yamlArraySteps);
    expect(result.current.model.steps).toHaveLength(3);
  });

  it('parses typed text after the delay', () => {
    const next = yamlArraySteps + '# typed\n';
    const { result, rerender } = renderHook(({ text }) => useDocModel(text), {
      initialProps: { text: yamlArraySteps },
    });

    rerender({ text: next });
    expect(result.current.model.text).toBe(yamlArraySteps);

    act(() => {
      vi.advanceTimersByTime(DOC_MODEL_DELAY_MS);
    });
    expect(result.current.model.text).toBe(next);
  });

  it('parses synced text without the delay', () => {
    const next = yamlArraySteps + '# from the canvas\n';
    const { result, rerender } = renderHook(({ text }) => useDocModel(text), {
      initialProps: { text: yamlArraySteps },
    });

    rerender({ text: next });
    act(() => result.current.sync(next));
    expect(result.current.model.text).toBe(next);
  });

  it('keeps the last valid model while the text does not parse', () => {
    const { result, rerender } = renderHook(({ text }) => useDocModel(text), {
      initialProps: { text: yamlArraySteps },
    });

    // A tab in the top level keys leaves no readable graph.
    const tabbed = `\tname: x\n${yamlArraySteps}`;
    rerender({ text: tabbed });
    act(() => {
      vi.advanceTimersByTime(DOC_MODEL_DELAY_MS);
    });
    expect(result.current.frozen).toBe(true);
    expect(result.current.model.ok).toBe(true);
    expect(result.current.model.text).toBe(yamlArraySteps);
    expect(result.current.model.steps).toHaveLength(3);

    // The fix shows after a short delay.
    rerender({ text: yamlArraySteps });
    act(() => {
      vi.advanceTimersByTime(FROZEN_MODEL_DELAY_MS);
    });
    expect(result.current.frozen).toBe(false);
    expect(result.current.model.text).toBe(yamlArraySteps);
  });

  it('keeps every other step while one step does not parse', () => {
    const { result, rerender } = renderHook(({ text }) => useDocModel(text), {
      initialProps: { text: yamlArraySteps },
    });

    const broken = yamlArraySteps.replace(/ {4}run: .*\n/, '    run: [a\n');
    expect(broken).not.toBe(yamlArraySteps);
    rerender({ text: broken });
    act(() => {
      vi.advanceTimersByTime(DOC_MODEL_DELAY_MS);
    });
    expect(result.current.frozen).toBe(false);
    expect(result.current.model.text).toBe(broken);
    expect(result.current.model.steps).toHaveLength(3);
    expect(
      result.current.model.steps.filter((step) => step.broken !== undefined)
    ).toHaveLength(1);

    rerender({ text: yamlArraySteps });
    act(() => {
      vi.advanceTimersByTime(FROZEN_MODEL_DELAY_MS);
    });
    expect(result.current.model.parseErrors).toEqual([]);
    expect(result.current.model.text).toBe(yamlArraySteps);
  });

  it('keeps an empty last valid model when the first text is broken', () => {
    const { result } = renderHook(() => useDocModel('\tname: x\n'));
    expect(result.current.frozen).toBe(true);
    expect(result.current.model.steps).toEqual([]);
  });
});

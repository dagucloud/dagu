// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { scopeHttpJq } from '../../testing/fixtures';
import {
  buildReferenceCandidates,
  registerReferenceCompletion,
  UpstreamDataPanel,
} from '..';

vi.mock('@/features/visual-editor/api/useStepOutputs', () => ({
  useStepOutputs: () => ({
    data: undefined,
    error: undefined,
    isLoading: false,
  }),
}));

describe('references module', () => {
  it('exports the candidate builder', () => {
    expect(buildReferenceCandidates(scopeHttpJq, 'extract')[0]?.token).toBe(
      '${steps.fetch.outputs.body}'
    );
  });

  it('exports the upstream data panel', () => {
    render(
      <UpstreamDataPanel
        dagName="fetch-items"
        runId={null}
        scope={scopeHttpJq}
        stepName="extract"
      />
    );
    expect(screen.queryByTestId('ve-stub-UpstreamDataPanel')).toBeNull();
    expect(screen.getByText('Upstream data')).toBeInTheDocument();
  });

  it('exports the YAML completion and hover registration', () => {
    const dispose = vi.fn();
    const languages = {
      registerCompletionItemProvider: vi.fn(() => ({ dispose })),
      registerHoverProvider: vi.fn(() => ({ dispose })),
    };
    const monaco = { languages } as unknown as Parameters<
      typeof registerReferenceCompletion
    >[0];
    registerReferenceCompletion(monaco, () => ({})).dispose();
    expect(languages.registerCompletionItemProvider).toHaveBeenCalledWith(
      'yaml',
      expect.anything()
    );
    expect(languages.registerHoverProvider).toHaveBeenCalledWith(
      'yaml',
      expect.anything()
    );
    expect(dispose).toHaveBeenCalledTimes(2);
  });
});

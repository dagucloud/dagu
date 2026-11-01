// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, render, renderHook } from '@testing-library/react';
import { useState, type ReactNode } from 'react';
import { describe, expect, it, vi } from 'vitest';
import {
  RightPanelProvider,
  useActiveRightPanel,
  useCloseRightPanel,
  useRightPanel,
  useRightPanelClaim,
  type RightPanelId,
} from '../RightPanelContext';

function usePanels(onYaml: () => void, onAddStep: () => void) {
  return {
    yaml: useRightPanel('yaml', onYaml),
    addStep: useRightPanel('add-step', onAddStep),
    active: useActiveRightPanel(),
  };
}

const withProvider = ({ children }: { children: ReactNode }) => (
  <RightPanelProvider>{children}</RightPanelProvider>
);

// parity:drawer-mode-rules
describe('RightPanelContext', () => {
  describe('with a provider', () => {
    // A part of the page that takes the room of the panel closes it, as a
    // panel of another id would.
    it('closes the active panel and tells it', () => {
      const onYaml = vi.fn();
      const { result } = renderHook(
        () => ({
          ...usePanels(onYaml, vi.fn()),
          close: useCloseRightPanel(),
        }),
        { wrapper: withProvider }
      );

      act(() => result.current.close());
      expect(onYaml).not.toHaveBeenCalled();

      act(() => result.current.yaml.claim());
      act(() => result.current.close());
      expect(onYaml).toHaveBeenCalledTimes(1);
      expect(result.current.active).toBeNull();
    });

    it('evicts the holder of another id synchronously on claim', () => {
      const onYaml = vi.fn();
      const onAddStep = vi.fn();
      const { result } = renderHook(() => usePanels(onYaml, onAddStep), {
        wrapper: withProvider,
      });

      act(() => result.current.yaml.claim());
      expect(result.current.active).toBe('yaml');
      expect(result.current.yaml.isActive).toBe(true);

      let calledDuringClaim = 0;
      act(() => {
        result.current.addStep.claim();
        calledDuringClaim = onYaml.mock.calls.length;
      });
      expect(calledDuringClaim).toBe(1);
      expect(onAddStep).not.toHaveBeenCalled();
      expect(result.current.active).toBe('add-step');
      expect(result.current.yaml.isActive).toBe(false);
      expect(result.current.addStep.isActive).toBe(true);
    });

    it('ignores a release by an id that is not active', () => {
      const { result } = renderHook(() => usePanels(vi.fn(), vi.fn()), {
        wrapper: withProvider,
      });

      act(() => result.current.addStep.claim());
      act(() => result.current.yaml.release());
      expect(result.current.active).toBe('add-step');

      act(() => result.current.addStep.release());
      expect(result.current.active).toBeNull();
    });

    it('does not evict on a second claim of the same id', () => {
      const onYaml = vi.fn();
      const { result } = renderHook(() => usePanels(onYaml, vi.fn()), {
        wrapper: withProvider,
      });

      act(() => result.current.yaml.claim());
      act(() => result.current.yaml.claim());
      expect(onYaml).not.toHaveBeenCalled();
      expect(result.current.active).toBe('yaml');
    });

    it('gives the place back when the active holder unmounts', () => {
      function Holder() {
        const { claim } = useRightPanel('workflow', () => {});
        return (
          <button type="button" onClick={claim}>
            Claim
          </button>
        );
      }
      function Active() {
        return <output>{useActiveRightPanel() ?? 'none'}</output>;
      }
      const view = render(
        <RightPanelProvider>
          <Holder />
          <Active />
        </RightPanelProvider>
      );
      act(() => view.getByText('Claim').click());
      expect(view.getByRole('status')).toHaveTextContent('workflow');

      view.rerender(
        <RightPanelProvider>
          <Active />
        </RightPanelProvider>
      );
      expect(view.getByRole('status')).toHaveTextContent('none');
    });
  });

  describe('without a provider', () => {
    it('lets two panels be active and never evicts', () => {
      const onYaml = vi.fn();
      const onAddStep = vi.fn();
      const { result } = renderHook(() => usePanels(onYaml, onAddStep));

      act(() => result.current.yaml.claim());
      act(() => result.current.addStep.claim());
      expect(result.current.yaml.isActive).toBe(true);
      expect(result.current.addStep.isActive).toBe(true);
      expect(result.current.active).toBeNull();
      expect(onYaml).not.toHaveBeenCalled();
      expect(onAddStep).not.toHaveBeenCalled();

      act(() => result.current.yaml.release());
      expect(result.current.yaml.isActive).toBe(false);
      expect(result.current.addStep.isActive).toBe(true);
    });
  });

  describe('useRightPanelClaim', () => {
    function Panel({ id, label }: { id: RightPanelId; label: string }) {
      const [open, setOpen] = useState(false);
      useRightPanelClaim(id, open, () => setOpen(false));
      return (
        <button type="button" onClick={() => setOpen(true)}>
          {label}:{open ? 'open' : 'closed'}
        </button>
      );
    }
    function Active() {
      return <output>{useActiveRightPanel() ?? 'none'}</output>;
    }

    it('closes the other panel when one opens', () => {
      const view = render(
        <RightPanelProvider>
          <Panel id="yaml" label="yaml" />
          <Panel id="workflow" label="workflow" />
          <Active />
        </RightPanelProvider>
      );

      act(() => view.getByText('yaml:closed').click());
      expect(view.getByRole('status')).toHaveTextContent('yaml');

      act(() => view.getByText('workflow:closed').click());
      expect(view.getByText('yaml:closed')).toBeInTheDocument();
      expect(view.getByText('workflow:open')).toBeInTheDocument();
      expect(view.getByRole('status')).toHaveTextContent('workflow');
    });

    it('releases the id when the panel unmounts', () => {
      const view = render(
        <RightPanelProvider>
          <Panel id="yaml" label="yaml" />
          <Active />
        </RightPanelProvider>
      );
      act(() => view.getByText('yaml:closed').click());
      expect(view.getByRole('status')).toHaveTextContent('yaml');

      view.rerender(
        <RightPanelProvider>
          <Active />
        </RightPanelProvider>
      );
      expect(view.getByRole('status')).toHaveTextContent('none');
    });
  });
});

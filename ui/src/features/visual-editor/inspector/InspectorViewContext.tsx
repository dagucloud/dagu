// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  createContext,
  useCallback,
  useContext,
  useMemo,
  useRef,
  useState,
  type MutableRefObject,
  type ReactElement,
  type ReactNode,
} from 'react';

/** The part of the step form that the node details view shows. */
export type InspectorSegment = 'parameters' | 'settings';

/**
 * How a host shows the step form. With `split`, the form has a
 * "Parameters | Settings" choice and no YAML section. `focusPath` is a field
 * path relative to the step (`with.url`) that the form shows and focuses
 * once, then clears.
 */
export interface InspectorView {
  split: boolean;
  segment: InspectorSegment;
  setSegment(segment: InspectorSegment): void;
  focusPath: string | null;
  setFocusPath(path: string | null): void;
  clearFocus(): void;
}

const noop = () => undefined;

/** The view without a host: the form shows as one list of sections. */
export const DEFAULT_INSPECTOR_VIEW: InspectorView = {
  split: false,
  segment: 'parameters',
  setSegment: noop,
  focusPath: null,
  setFocusPath: noop,
  clearFocus: noop,
};

export const InspectorViewContext = createContext<InspectorView>(
  DEFAULT_INSPECTOR_VIEW
);

/**
 * The identities of the steps of the last model that the form showed. It
 * outlives the form, which unmounts while the list of steps is open, so
 * that the form can tell a new step from an existing one.
 */
export type StepMemory = MutableRefObject<ReadonlySet<string> | null>;

const StepMemoryContext = createContext<StepMemory | null>(null);

export function useInspectorView(): InspectorView {
  return useContext(InspectorViewContext);
}

/** The step memory of the host, or null without a provider. */
export function useStepMemory(): StepMemory | null {
  return useContext(StepMemoryContext);
}

/** The state of a split view, for a host that also uses it outside the provider. */
export function useInspectorViewState(): InspectorView {
  const [segment, setSegment] = useState<InspectorSegment>('parameters');
  const [focusPath, setFocusPath] = useState<string | null>(null);
  const clearFocus = useCallback(() => setFocusPath(null), []);
  return useMemo(
    () => ({
      split: true,
      segment,
      setSegment,
      focusPath,
      setFocusPath,
      clearFocus,
    }),
    [segment, focusPath, clearFocus]
  );
}

/**
 * Gives the step form of the children a split view and a step memory. A
 * null value provides nothing: the children keep the default view.
 */
export function InspectorViewProvider({
  value,
  children,
}: {
  value: InspectorView | null;
  children: ReactNode;
}): ReactElement {
  const memory = useRef<ReadonlySet<string> | null>(null);
  if (value === null) {
    return <>{children}</>;
  }
  return (
    <InspectorViewContext.Provider value={value}>
      <StepMemoryContext.Provider value={memory}>
        {children}
      </StepMemoryContext.Provider>
    </InspectorViewContext.Provider>
  );
}

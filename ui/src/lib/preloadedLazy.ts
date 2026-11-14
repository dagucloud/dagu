// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { lazy, type ComponentType, type LazyExoticComponent } from 'react';

/**
 * A lazy component and the loader of its module. Once the loader has
 * resolved, the component renders in its first render. React.lazy alone
 * suspends there even for a module that is loaded, and React holds the
 * reveal of a boundary that showed its fallback for up to 300 ms.
 */
export function preloadedLazy<P extends object>(
  load: () => Promise<{ default: ComponentType<P> }>
): {
  Component: LazyExoticComponent<ComponentType<P>>;
  preload: () => Promise<{ default: ComponentType<P> }>;
} {
  let loaded: { default: ComponentType<P> } | undefined;
  const preload = () =>
    load().then((module) => {
      loaded = module;
      return module;
    });
  const Component = lazy(() => (loaded ? resolvedThenable(loaded) : preload()));
  return { Component, preload };
}

/**
 * A thenable that hands its value to `then` at once: React.lazy reads a
 * module that comes so in the same render, where a promise would come in a
 * later task.
 */
function resolvedThenable<T>(value: T): Promise<T> {
  return {
    then(onFulfilled: (value: T) => unknown) {
      onFulfilled(value);
    },
  } as unknown as Promise<T>;
}

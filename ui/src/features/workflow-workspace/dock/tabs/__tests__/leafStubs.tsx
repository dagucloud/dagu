// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect } from 'react';

type Props = Record<string, unknown>;

/** The last props and the mount count of each stubbed leaf, by name. */
export const leaves = {
  props: {} as Record<string, Props>,
  mounts: {} as Record<string, number>,
  reset() {
    leaves.props = {};
    leaves.mounts = {};
  },
};

/**
 * A stand-in for a shared leaf that has its own tests. It records its props
 * and mounts; a button calls its onChanged prop when it has one.
 */
export function leaf(name: string) {
  function Leaf(props: Props) {
    leaves.props[name] = props;
    useEffect(() => {
      leaves.mounts[name] = (leaves.mounts[name] ?? 0) + 1;
    }, []);
    const onChanged = props.onChanged as (() => unknown) | undefined;
    return (
      <div data-testid={`leaf-${name}`}>
        {onChanged && (
          <button type="button" onClick={() => void onChanged()}>
            {`${name} changed`}
          </button>
        )}
      </div>
    );
  }
  Leaf.displayName = `Leaf(${name})`;
  return Leaf;
}

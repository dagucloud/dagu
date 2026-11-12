// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ReactElement } from 'react';
import { Link, type LinkProps } from 'react-router-dom';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { peekPageURL, type PeekTarget } from './peekParams';
import { useShowPeek } from './usePeekOrOpen';

type Props = Omit<LinkProps, 'to'> & {
  /** What the link opens; `target` stays the attribute of the anchor. */
  peek: PeekTarget;
  remoteNode?: string;
};

/**
 * A link to the full page of a peek target. A plain primary click shows it
 * where one can show; a middle, Ctrl, Cmd, Shift or Alt click keeps the
 * native behavior of the link.
 */
export function PeekLink({
  peek,
  remoteNode,
  onClick,
  ...props
}: Props): ReactElement {
  const node = useRemoteNode(remoteNode);
  const showPeek = useShowPeek();
  return (
    <Link
      {...props}
      to={peekPageURL(peek, node)}
      onClick={(event) => {
        onClick?.(event);
        if (
          event.defaultPrevented ||
          event.button !== 0 ||
          event.ctrlKey ||
          event.metaKey ||
          event.shiftKey ||
          event.altKey
        ) {
          return;
        }
        if (showPeek(peek)) event.preventDefault();
      }}
    />
  );
}

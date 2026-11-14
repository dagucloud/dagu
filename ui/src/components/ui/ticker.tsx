import React from 'react';
import { useClockNow } from '@/hooks/useClock';

interface Props {
  intervalMs: number;
  children: () => React.JSX.Element;
}

/** Renders `children` again every `intervalMs`, on the shared clock. */
export default function Ticker({ intervalMs, children }: Props) {
  useClockNow(intervalMs);
  return children?.();
}

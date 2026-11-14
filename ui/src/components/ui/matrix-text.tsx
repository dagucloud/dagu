import React from 'react';

import { useClock } from '@/hooks/useClock';
import { usePrefersReducedMotion } from '@/hooks/usePrefersReducedMotion';
import { cn } from '@/lib/utils';

type MatrixTextProps = {
  text: string;
  className?: string;
  glowSpeed?: number;
};

function MatrixText({ text, className, glowSpeed = 175 }: MatrixTextProps) {
  const reducedMotion = usePrefersReducedMotion();
  const textChars = text.split('');
  // The glow reads the shared clock: all chips glow in step, on one timer,
  // and none runs while the tab is hidden.
  const glowPosition = useClock(
    reducedMotion || textChars.length === 0 ? null : glowSpeed,
    (now) =>
      reducedMotion || textChars.length === 0
        ? 0
        : Math.floor(now / glowSpeed) % textChars.length
  );

  return (
    <span
      className={cn('inline-flex', className)}
      aria-label={text}
      role="status"
    >
      <span className="sr-only">{text}</span>
      <span aria-hidden="true">
        {textChars.map((char, index) => (
          <span
            key={index}
            className="inline-block transition-colors duration-200"
            style={index === glowPosition ? { color: '#86efac' } : undefined}
          >
            {char}
          </span>
        ))}
      </span>
    </span>
  );
}

export type { MatrixTextProps };
export { MatrixText };
export default MatrixText;

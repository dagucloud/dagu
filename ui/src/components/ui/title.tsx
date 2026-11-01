import { cn } from '@/lib/utils';
import * as React from 'react';

interface TitleProps {
  children?: React.ReactNode;
  className?: string;
}

export default function Title({ children, className }: TitleProps) {
  return (
    <h1
      className={cn(
        'text-2xl font-semibold tracking-[-0.01em] text-foreground',
        className
      )}
    >
      {children}
    </h1>
  );
}

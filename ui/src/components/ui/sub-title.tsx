import { cn } from '@/lib/utils';
import * as React from 'react';

interface SubTitleProps {
  children?: React.ReactNode;
  className?: string;
}

export default function SubTitle({ children, className }: SubTitleProps) {
  return (
    <h3 className={cn('text-lg font-semibold text-foreground mb-3', className)}>
      {children}
    </h3>
  );
}

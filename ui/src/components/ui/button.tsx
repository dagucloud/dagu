import { Slot } from '@radix-ui/react-slot';
import { cva, type VariantProps } from 'class-variance-authority';
import * as React from 'react';

import { cn } from '@/lib/utils';

const buttonVariants = cva(
  'inline-flex shrink-0 cursor-pointer items-center justify-center gap-1.5 whitespace-nowrap rounded-md border text-sm font-medium outline-none transition-colors duration-150 focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background disabled:pointer-events-none disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:size-4 [&_svg]:shrink-0',
  {
    variants: {
      variant: {
        default:
          'border-input bg-card text-foreground shadow-sm hover:border-border-strong hover:bg-muted',
        primary:
          'border-primary-fill bg-primary-fill text-primary-foreground shadow-sm hover:border-primary-fill-hover hover:bg-primary-fill-hover',
        destructive:
          'border-destructive bg-destructive text-destructive-foreground shadow-sm hover:opacity-90',
        outline:
          'border-input bg-card text-foreground shadow-sm hover:border-border-strong hover:bg-muted',
        secondary:
          'border-transparent bg-secondary text-secondary-foreground hover:bg-accent',
        ghost:
          'border-transparent bg-transparent text-muted-foreground hover:bg-muted hover:text-foreground',
        link: 'border-transparent bg-transparent text-primary hover:text-primary-hover hover:underline',
      },
      size: {
        default: 'h-9 px-4',
        sm: 'h-8 px-3 text-xs',
        xs: 'h-7 px-2 text-xs',
        lg: 'h-10 px-5',
        icon: 'size-9 p-0',
        'icon-sm': 'size-7 p-0',
      },
    },
    defaultVariants: {
      variant: 'default',
      size: 'default',
    },
  }
);

function Button({
  className,
  variant,
  size,
  asChild = false,
  ...props
}: React.ComponentProps<'button'> &
  VariantProps<typeof buttonVariants> & {
    asChild?: boolean;
  }) {
  const Comp = asChild ? Slot : 'button';

  return (
    <Comp
      data-slot="button"
      className={cn(buttonVariants({ variant, size, className }))}
      {...props}
    />
  );
}

export { Button, buttonVariants };

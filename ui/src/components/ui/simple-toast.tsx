// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { AlertCircle, Check, Info, X } from 'lucide-react';
import React, { useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { useI18n } from '@/i18n/I18nProvider';
import { getNotificationRoot } from './notification-root';

export type ToastVariant = 'success' | 'error' | 'info';

type ToastOptions = {
  duration?: number;
  variant?: ToastVariant;
};

interface SimpleToastProps {
  message: string;
  duration?: number;
  variant?: ToastVariant;
  onClose?: () => void;
}

// Minimum duration, so that a note is never gone before it can be read.
const MIN_DURATION = 500;
const MAX_STACKED_TOASTS = 5;

const DEFAULT_DURATIONS: Record<ToastVariant, number> = {
  success: 1800,
  info: 4000,
  error: 6000,
};

const VARIANT_STYLES: Record<
  ToastVariant,
  { iconClass: string; icon: React.ElementType }
> = {
  success: { iconClass: 'text-success', icon: Check },
  error: { iconClass: 'text-destructive', icon: AlertCircle },
  info: { iconClass: 'text-info', icon: Info },
};

/**
 * A note in the bottom right corner, in the stack of the notifications (the
 * run progress cards are in it too). It goes away by itself after its
 * duration, or at once with its close button.
 */
export const SimpleToast: React.FC<SimpleToastProps> = ({
  message,
  duration,
  variant = 'info',
  onClose,
}) => {
  const safeDuration = Math.max(
    duration ?? DEFAULT_DURATIONS[variant],
    MIN_DURATION
  );
  // The timer does not start again when the host passes a new function.
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;
  useEffect(() => {
    const timer = setTimeout(() => onCloseRef.current?.(), safeDuration);
    return () => clearTimeout(timer);
  }, [safeDuration]);

  const { ts } = useI18n();
  const { iconClass, icon: IconComponent } = VARIANT_STYLES[variant];

  return createPortal(
    <div
      role={variant === 'error' ? 'alert' : 'status'}
      data-testid="toast"
      data-variant={variant}
      className="pointer-events-auto relative flex items-start gap-2 overflow-hidden rounded-lg border border-border bg-card px-4 py-3 pr-10 text-sm text-card-foreground shadow-lg"
    >
      <IconComponent
        aria-hidden="true"
        className={`mt-0.5 h-4 w-4 shrink-0 ${iconClass}`}
      />
      <span className="min-w-0 flex-1 break-words">{message}</span>
      <button
        type="button"
        aria-label={ts('Dismiss')}
        className="absolute right-2 top-2 inline-flex size-6 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        onClick={() => onCloseRef.current?.()}
      >
        <X aria-hidden="true" className="h-4 w-4" />
      </button>
    </div>,
    getNotificationRoot()
  );
};

interface ToastManagerProps {
  children: React.ReactNode;
}

interface ToastContextType {
  showToast: (message: string, options?: ToastOptions) => void;
}

export const ToastContext = React.createContext<ToastContextType>({
  showToast: () => {},
});

interface ToastItem {
  id: number;
  message: string;
  duration?: number;
  variant: ToastVariant;
}

let nextToastId = 0;

export const ToastProvider: React.FC<ToastManagerProps> = ({ children }) => {
  const [toasts, setToasts] = useState<ToastItem[]>([]);

  const showToast = (message: string, options?: ToastOptions) => {
    const variant = options?.variant ?? 'success';
    // A successful action shows nothing: the page already shows its result.
    // Only an error or a note shows, in the corner.
    if (variant === 'success') return;
    nextToastId += 1;
    const item: ToastItem = {
      id: nextToastId,
      message,
      duration: options?.duration,
      variant,
    };
    setToasts((current) => [item, ...current].slice(0, MAX_STACKED_TOASTS));
  };

  const close = React.useCallback((id: number) => {
    setToasts((current) => current.filter((toast) => toast.id !== id));
  }, []);

  return (
    <ToastContext.Provider value={{ showToast }}>
      {children}
      {toasts.map((toast) => (
        <ToastHost key={toast.id} toast={toast} onClose={close} />
      ))}
    </ToastContext.Provider>
  );
};

function ToastHost({
  toast,
  onClose,
}: {
  toast: ToastItem;
  onClose: (id: number) => void;
}) {
  const close = React.useCallback(() => onClose(toast.id), [onClose, toast.id]);
  return (
    <SimpleToast
      message={toast.message}
      duration={toast.duration}
      variant={toast.variant}
      onClose={close}
    />
  );
}

export const useSimpleToast = () => {
  const context = React.useContext(ToastContext);
  if (context === undefined) {
    throw new Error('useSimpleToast must be used within a ToastProvider');
  }
  return context;
};

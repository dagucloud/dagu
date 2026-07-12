// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { AlertCircle, Check, Info } from 'lucide-react';
import React, { useEffect, useState } from 'react';
import { createPortal } from 'react-dom';

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

// Minimum duration to ensure animations complete properly
const MIN_DURATION = 500;

const DEFAULT_DURATIONS: Record<ToastVariant, number> = {
  success: 1800,
  info: 2200,
  error: 3500,
};

const VARIANT_STYLES: Record<
  ToastVariant,
  { circleClass: string; iconClass: string; icon: React.ElementType }
> = {
  success: {
    circleClass: 'border-success',
    iconClass: 'text-success',
    icon: Check,
  },
  error: {
    circleClass: 'border-destructive',
    iconClass: 'text-destructive',
    icon: AlertCircle,
  },
  info: { circleClass: 'border-info', iconClass: 'text-info', icon: Info },
};

export const SimpleToast: React.FC<SimpleToastProps> = ({
  message,
  duration,
  variant = 'success',
  onClose,
}) => {
  const [isVisible, setIsVisible] = useState(true);
  const [animationState, setAnimationState] = useState<
    'entering' | 'visible' | 'exiting'
  >('entering');
  const [iconAnimated, setIconAnimated] = useState(false);

  // Ensure duration is at least MIN_DURATION
  const safeDuration = Math.max(
    duration ?? DEFAULT_DURATIONS[variant],
    MIN_DURATION
  );

  useEffect(() => {
    // Enter animation
    const enterTimer = setTimeout(() => {
      setAnimationState('visible');
    }, 20);

    // Icon draw animation
    const iconTimer = setTimeout(() => {
      setIconAnimated(true);
    }, 150);

    // Start exit animation (at least 350ms before end)
    const exitTimer = setTimeout(() => {
      setAnimationState('exiting');
    }, safeDuration - 350);

    // Remove from DOM
    const removeTimer = setTimeout(() => {
      setIsVisible(false);
      if (onClose) onClose();
    }, safeDuration);

    return () => {
      clearTimeout(enterTimer);
      clearTimeout(iconTimer);
      clearTimeout(exitTimer);
      clearTimeout(removeTimer);
    };
  }, [safeDuration, onClose]);

  if (!isVisible) return null;

  const getAnimationClasses = () => {
    switch (animationState) {
      case 'entering':
        return 'opacity-0 scale-90';
      case 'visible':
        return 'opacity-100 scale-100';
      case 'exiting':
        return 'opacity-0 scale-95';
    }
  };

  const {
    circleClass,
    iconClass,
    icon: IconComponent,
  } = VARIANT_STYLES[variant];

  return createPortal(
    <div className="fixed inset-0 z-[100] flex items-center justify-center pointer-events-none">
      <div
        className={`
          pointer-events-auto
          flex flex-col items-center justify-center gap-3
          w-32 h-32
          bg-popover/80
          backdrop-blur-xl
          rounded-[20px]
          border border-border/50
          shadow-toast
          transition-all duration-300 ease-out
          ${getAnimationClasses()}
        `}
      >
        {/* Animated icon circle */}
        <div className="relative w-12 h-12">
          {/* Circle background */}
          <div
            className={`
              absolute inset-0 rounded-full
              border-[2.5px] ${circleClass}
              transition-all duration-300 ease-out
              ${iconAnimated ? 'opacity-100 scale-100' : 'opacity-0 scale-75'}
            `}
          />
          {/* Variant icon */}
          <div
            className={`
              absolute inset-0 flex items-center justify-center
              transition-all duration-300 ease-out delay-100
              ${iconAnimated ? 'opacity-100 scale-100' : 'opacity-0 scale-50'}
            `}
          >
            <IconComponent className={`h-7 w-7 ${iconClass}`} strokeWidth={3} />
          </div>
        </div>

        {/* Message */}
        <span
          className={`
            text-sm font-medium text-foreground/90 text-center px-2
            transition-all duration-200 delay-150
            ${iconAnimated ? 'opacity-100' : 'opacity-0'}
          `}
        >
          {message}
        </span>
      </div>
    </div>,
    document.body
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

export const ToastProvider: React.FC<ToastManagerProps> = ({ children }) => {
  const [toast, setToast] = useState<{
    message: string;
    duration?: number;
    variant: ToastVariant;
    id: number;
  } | null>(null);

  const showToast = (message: string, options?: ToastOptions) => {
    setToast({
      message,
      duration: options?.duration,
      variant: options?.variant ?? 'success',
      id: Date.now(),
    });
  };

  const handleClose = () => {
    setToast(null);
  };

  return (
    <ToastContext.Provider value={{ showToast }}>
      {children}
      {toast && (
        <SimpleToast
          key={toast.id}
          message={toast.message}
          duration={toast.duration}
          variant={toast.variant}
          onClose={handleClose}
        />
      )}
    </ToastContext.Provider>
  );
};

export const useSimpleToast = () => {
  const context = React.useContext(ToastContext);
  if (context === undefined) {
    throw new Error('useSimpleToast must be used within a ToastProvider');
  }
  return context;
};

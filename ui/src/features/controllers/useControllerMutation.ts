// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import * as React from 'react';

import { useSimpleToast } from '@/components/ui/simple-toast';

export function useControllerMutation(
  refresh: () => Promise<unknown>,
  refreshError: string,
  scopeKey: string
) {
  const { showToast } = useSimpleToast();
  const mountedRef = React.useRef(false);
  const committedScopeRef = React.useRef({ key: scopeKey, generation: 0 });
  const operationRef = React.useRef<{
    scopeKey: string;
    scopeGeneration: number;
  } | null>(null);
  const [state, setState] = React.useState({
    scopeGeneration: 0,
    pending: false,
    error: null as string | null,
  });

  React.useLayoutEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      operationRef.current = null;
    };
  }, []);

  React.useLayoutEffect(() => {
    if (committedScopeRef.current.key !== scopeKey) {
      committedScopeRef.current = {
        key: scopeKey,
        generation: committedScopeRef.current.generation + 1,
      };
    }
  }, [scopeKey]);

  const setError = (value: React.SetStateAction<string | null>) => {
    const scope = committedScopeRef.current;
    if (scope.key !== scopeKey) return;
    setState((current) => {
      const error =
        typeof value === 'function'
          ? value(
              current.scopeGeneration === scope.generation
                ? current.error
                : null
            )
          : value;
      return {
        scopeGeneration: scope.generation,
        pending:
          current.scopeGeneration === scope.generation
            ? current.pending
            : false,
        error,
      };
    });
  };

  const run = async (action: () => Promise<void>, success: string) => {
    const scope = committedScopeRef.current;
    if (scope.key !== scopeKey) return false;
    if (operationRef.current?.scopeGeneration === scope.generation) {
      return false;
    }
    const operation = {
      scopeKey: scope.key,
      scopeGeneration: scope.generation,
    };
    operationRef.current = operation;
    const isCurrent = () =>
      mountedRef.current &&
      operationRef.current === operation &&
      committedScopeRef.current.key === operation.scopeKey &&
      committedScopeRef.current.generation === operation.scopeGeneration;
    setState({
      scopeGeneration: operation.scopeGeneration,
      pending: true,
      error: null,
    });
    try {
      try {
        await action();
      } catch (failure) {
        if (isCurrent()) {
          setState({
            scopeGeneration: operation.scopeGeneration,
            pending: true,
            error:
              failure instanceof Error
                ? failure.message
                : 'Controller action failed',
          });
        }
        return false;
      }

      if (!isCurrent()) return false;
      showToast(success);
      if (!isCurrent()) return false;
      try {
        await refresh();
      } catch {
        if (!isCurrent()) return false;
        setState({
          scopeGeneration: operation.scopeGeneration,
          pending: true,
          error: refreshError,
        });
      }
      return isCurrent();
    } finally {
      if (isCurrent()) {
        operationRef.current = null;
        setState((current) =>
          current.scopeGeneration === operation.scopeGeneration
            ? { ...current, pending: false }
            : current
        );
      }
    }
  };

  const committedScope = committedScopeRef.current;
  const stateIsCurrent =
    committedScope.key === scopeKey &&
    state.scopeGeneration === committedScope.generation;
  return {
    pending: stateIsCurrent ? state.pending : false,
    error: stateIsCurrent ? state.error : null,
    setError,
    run,
  };
}

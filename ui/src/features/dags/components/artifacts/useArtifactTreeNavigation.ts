// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React, {
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from 'react';

type TreeNode = {
  name: string;
  path: string;
  type: 'directory' | 'file';
  children?: TreeNode[];
};

type Options = {
  nodes: TreeNode[];
  selectedPath: string | null;
  onSelect: (path: string) => void;
  scope: string;
  ready: boolean;
  autoFocus?: boolean;
  onSearch?: () => void;
};

export function useArtifactTreeNavigation({
  nodes,
  selectedPath,
  onSelect,
  scope,
  ready,
  autoFocus = false,
  onSearch,
}: Options) {
  const [state, setState] = useState({
    scope,
    expanded: new Set<string>(),
    focused: null as string | null,
    revealed: null as string | null,
  });
  const treeRef = useRef<HTMLDivElement>(null);
  const previewRef = useRef<HTMLDivElement>(null);
  const elements = useRef(new Map<string, HTMLDivElement>());
  const ownsFocus = useRef(false);
  const initialFocusPending = useRef(autoFocus);
  const previousParents = useRef(new Map<string, string | null>());

  if (state.scope !== scope) {
    setState({ scope, expanded: new Set(), focused: null, revealed: null });
  }

  const { entries, parents } = useMemo(() => {
    const entries = new Map<string, TreeNode>();
    const parents = new Map<string, string | null>();
    function visit(children: TreeNode[], parent: string | null) {
      for (const node of children) {
        entries.set(node.path, node);
        parents.set(node.path, parent);
        visit(node.children ?? [], node.path);
      }
    }
    visit(nodes, null);
    return { entries, parents };
  }, [nodes]);

  const visible = useMemo(() => {
    const rows: TreeNode[] = [];
    function visit(children: TreeNode[]) {
      for (const node of children) {
        rows.push(node);
        if (state.expanded.has(node.path)) {
          visit(node.children ?? []);
        }
      }
    }
    visit(nodes);
    return rows;
  }, [nodes, state.expanded]);

  const visiblePaths = new Set(visible.map((node) => node.path));
  let focusedPath = state.focused ?? selectedPath;
  while (focusedPath && !visiblePaths.has(focusedPath)) {
    focusedPath =
      parents.get(focusedPath) ??
      previousParents.current.get(focusedPath) ??
      null;
  }
  focusedPath ??= visible[0]?.path ?? null;

  // Reveal a newly selected file without undoing explicit folder collapses.
  useLayoutEffect(() => {
    if (!ready || state.revealed === selectedPath) {
      return;
    }
    if (!selectedPath) {
      setState((current) => ({ ...current, revealed: null }));
      return;
    }
    if (!entries.has(selectedPath)) {
      return;
    }
    setState((current) => {
      const expanded = new Set(current.expanded);
      let parent = parents.get(selectedPath);
      while (parent) {
        expanded.add(parent);
        parent = parents.get(parent);
      }
      return { ...current, expanded, revealed: selectedPath };
    });
  }, [entries, parents, ready, selectedPath, state.revealed]);

  useEffect(() => {
    const cancelInitialFocus = () => {
      initialFocusPending.current = false;
    };
    document.addEventListener('pointerdown', cancelInitialFocus, true);
    document.addEventListener('keydown', cancelInitialFocus, true);
    document.addEventListener('focusin', cancelInitialFocus, true);
    return () => {
      document.removeEventListener('pointerdown', cancelInitialFocus, true);
      document.removeEventListener('keydown', cancelInitialFocus, true);
      document.removeEventListener('focusin', cancelInitialFocus, true);
    };
  }, []);

  useLayoutEffect(() => {
    if (!ready) {
      return;
    }
    const initialTarget =
      selectedPath ??
      ([...entries.values()].some((node) => node.type === 'file')
        ? null
        : focusedPath);
    if (
      initialFocusPending.current &&
      initialTarget &&
      elements.current.has(initialTarget)
    ) {
      initialFocusPending.current = false;
      elements.current.get(initialTarget)?.focus({ preventScroll: true });
    } else if (
      ownsFocus.current &&
      focusedPath &&
      document.activeElement !== elements.current.get(focusedPath)
    ) {
      elements.current.get(focusedPath)?.focus({ preventScroll: true });
    }
    previousParents.current = parents;
  });

  const focus = (path: string | null) => {
    if (path) {
      elements.current.get(path)?.focus({ preventScroll: true });
    }
  };

  const toggle = (path: string) => {
    setState((current) => {
      const expanded = new Set(current.expanded);
      if (expanded.has(path)) {
        expanded.delete(path);
      } else {
        expanded.add(path);
      }
      return { ...current, expanded };
    });
  };

  const onKeyDown = (event: React.KeyboardEvent<HTMLDivElement>) => {
    if (
      event.defaultPrevented ||
      event.nativeEvent.isComposing ||
      event.altKey ||
      event.ctrlKey ||
      event.metaKey ||
      event.shiftKey ||
      !(event.target instanceof HTMLElement) ||
      event.target.getAttribute('role') !== 'treeitem'
    ) {
      return;
    }
    const index = visible.findIndex((node) => node.path === focusedPath);
    const node = visible[index];
    if (!node) {
      return;
    }
    switch (event.key) {
      case 'ArrowDown':
      case 'j':
        focus(visible[Math.min(index + 1, visible.length - 1)]!.path);
        break;
      case 'ArrowUp':
      case 'k':
        focus(visible[Math.max(index - 1, 0)]!.path);
        break;
      case 'Home':
        focus(visible[0]!.path);
        break;
      case 'End':
        focus(visible[visible.length - 1]!.path);
        break;
      case 'ArrowRight':
        if (node.type === 'directory') {
          if (state.expanded.has(node.path)) {
            focus(node.children?.[0]?.path ?? null);
          } else {
            toggle(node.path);
          }
        }
        break;
      case 'ArrowLeft':
        if (node.type === 'directory' && state.expanded.has(node.path)) {
          toggle(node.path);
        } else {
          focus(parents.get(node.path) ?? null);
        }
        break;
      case 'Enter':
      case ' ':
        if (node.type === 'directory') {
          toggle(node.path);
        } else {
          onSelect(node.path);
          if (event.key === 'Enter') {
            previewRef.current?.focus();
          }
        }
        break;
      case '/':
        if (!onSearch) {
          return;
        }
        onSearch();
        break;
      default:
        return;
    }
    event.preventDefault();
    event.stopPropagation();
  };

  return {
    focusedPath,
    expandedPaths: state.expanded,
    previewRef,
    returnToTree: () => focus(focusedPath),
    treeProps: {
      ref: treeRef,
      role: 'tree',
      onKeyDown,
      onFocus: (event: React.FocusEvent<HTMLDivElement>) => {
        ownsFocus.current = event.target.getAttribute('role') === 'treeitem';
      },
      onBlur: (event: React.FocusEvent<HTMLDivElement>) => {
        if (!event.currentTarget.contains(event.relatedTarget)) {
          ownsFocus.current = false;
        }
      },
    },
    getItemProps: (node: TreeNode) => ({
      ref: (element: HTMLDivElement | null) => {
        if (element) {
          elements.current.set(node.path, element);
        } else {
          elements.current.delete(node.path);
        }
      },
      role: 'treeitem',
      tabIndex: node.path === focusedPath ? 0 : -1,
      'aria-label': node.name,
      'aria-expanded':
        node.type === 'directory' ? state.expanded.has(node.path) : undefined,
      'aria-selected':
        node.type === 'file' ? selectedPath === node.path : undefined,
      className:
        'outline-none [&:focus-visible>div:first-child]:ring-2 [&:focus-visible>div:first-child]:ring-inset [&:focus-visible>div:first-child]:ring-ring',
      onClick: (event: React.MouseEvent<HTMLDivElement>) => {
        if (
          !(event.target instanceof Element) ||
          event.target.closest('[role="treeitem"]') !== event.currentTarget ||
          event.target.closest('a, button, input')
        ) {
          return;
        }
        focus(node.path);
        if (node.type === 'directory') {
          toggle(node.path);
        }
      },
      onFocus: (event: React.FocusEvent<HTMLDivElement>) => {
        if (event.target !== event.currentTarget) {
          return;
        }
        ownsFocus.current = true;
        setState((current) => ({ ...current, focused: node.path }));
        event.currentTarget.firstElementChild?.scrollIntoView({
          block: 'nearest',
        });
        if (node.type === 'file' && selectedPath !== node.path) {
          onSelect(node.path);
        }
      },
    }),
  };
}

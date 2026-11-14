// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import * as React from 'react';

// The dialog loads on first open so the shell does not pay for it upfront.
const CommandPaletteDialog = React.lazy(() => import('./CommandPaletteDialog'));

// Editors and terminals keep their own Cmd/Ctrl+K chords.
const KEYBOARD_OWNER_SELECTOR = '.monaco-editor, .xterm, .cm-editor';

type CommandPaletteContextValue = { open: () => void };

/** A command that a page adds to the palette while it is mounted. */
export interface PaletteCommand {
  id: string;
  label: string;
  keywords?: string[];
  /** Display text only; the palette does not bind it. */
  shortcut?: string;
  icon?: React.ReactNode;
  disabled?: boolean;
  /**
   * Read each time the palette opens. false hides the command; use it for a
   * state that the page does not render, such as a control in the DOM.
   */
  available?: () => boolean;
  run(): void;
}

export interface PaletteGroup {
  id: 'workflow';
  label: string;
}

export interface RegisteredPaletteGroup {
  group: PaletteGroup;
  commands: PaletteCommand[];
}

type PaletteRegistry = {
  register: (entry: RegisteredPaletteGroup) => () => void;
};

const PaletteRegistryContext = React.createContext<PaletteRegistry | null>(
  null
);

const CommandPaletteContext = React.createContext<CommandPaletteContextValue>({
  open: () => undefined,
});

/** Opens the command palette from any control inside its provider. */
export function useCommandPalette(): CommandPaletteContextValue {
  return React.useContext(CommandPaletteContext);
}

/**
 * Shows the commands in the palette, under the group label, while the caller
 * is mounted. null registers nothing. Outside the provider it does nothing.
 * Give a memoized array: each new array registers the group again.
 */
export function usePaletteCommands(
  group: PaletteGroup,
  commands: PaletteCommand[] | null
): void {
  const registry = React.useContext(PaletteRegistryContext);
  const { id, label } = group;
  React.useEffect(() => {
    if (!registry || !commands) {
      return;
    }
    return registry.register({ group: { id, label }, commands });
  }, [registry, id, label, commands]);
}

/** Display text for the palette shortcut on the current platform. */
export function commandPaletteShortcut(): string {
  const platform =
    typeof navigator === 'undefined' ? '' : navigator.platform || '';
  return /mac|iphone|ipad/i.test(platform) ? '⌘K' : 'Ctrl K';
}

/**
 * Provides the command palette and its Cmd/Ctrl+K shortcut. Must render
 * inside the router.
 */
export function CommandPaletteProvider({
  children,
}: {
  children: React.ReactNode;
}): React.ReactElement {
  const [isOpen, setIsOpen] = React.useState(false);
  const [hasOpened, setHasOpened] = React.useState(false);
  const [groups, setGroups] = React.useState<RegisteredPaletteGroup[]>([]);
  const registry = React.useMemo<PaletteRegistry>(
    () => ({
      register: (entry) => {
        setGroups((current) => [...current, entry]);
        return () =>
          setGroups((current) => current.filter((item) => item !== entry));
      },
    }),
    []
  );
  const open = React.useCallback(() => {
    setHasOpened(true);
    setIsOpen(true);
  }, []);

  React.useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (
        event.key.toLowerCase() !== 'k' ||
        !(event.metaKey || event.ctrlKey) ||
        event.altKey ||
        event.shiftKey
      ) {
        return;
      }
      const target = event.target;
      if (
        target instanceof Element &&
        target.closest(KEYBOARD_OWNER_SELECTOR)
      ) {
        return;
      }
      event.preventDefault();
      setHasOpened(true);
      setIsOpen((current) => !current);
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, []);

  const value = React.useMemo(() => ({ open }), [open]);

  return (
    <CommandPaletteContext.Provider value={value}>
      <PaletteRegistryContext.Provider value={registry}>
        {children}
      </PaletteRegistryContext.Provider>
      {hasOpened && (
        <React.Suspense fallback={null}>
          <CommandPaletteDialog
            open={isOpen}
            onOpenChange={setIsOpen}
            groups={groups}
          />
        </React.Suspense>
      )}
    </CommandPaletteContext.Provider>
  );
}

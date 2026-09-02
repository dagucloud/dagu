// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { useWikiPageTabContext } from '@/contexts/WikiPageTabContext';
import { cn } from '@/lib/utils';
import { MoreHorizontal, Trash2, X, XCircle } from 'lucide-react';
import React, { useCallback, useRef } from 'react';
import { I18nProps } from '@/i18n/I18nProps';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nText } from '@/i18n/I18nText';

type Props = {
  className?: string;
  onCloseTabWithUnsaved?: (tabId: string) => void;
  onDeleteWikiPage?: (
    wikiPagePath: string,
    title: string,
    workspace?: string | null
  ) => void;
  onCloseAllTabs?: () => void;
  onCloseOtherTabs?: (keepTabId: string) => void;
};

function WikiPageTabBar({
  className,
  onCloseTabWithUnsaved,
  onDeleteWikiPage,
  onCloseAllTabs,
  onCloseOtherTabs,
}: Props) {
  const { ts } = useI18n();
  const { tabs, activeTabId, closeTab, setActiveTab, isTabUnsaved } =
    useWikiPageTabContext();
  const tabRefs = useRef(new Map<string, HTMLDivElement>());

  const handleTabClick = (tabId: string) => {
    setActiveTab(tabId);
  };

  const handleCloseTab = useCallback(
    (e: React.MouseEvent, tabId: string) => {
      e.stopPropagation();
      if (isTabUnsaved(tabId) && onCloseTabWithUnsaved) {
        onCloseTabWithUnsaved(tabId);
      } else {
        closeTab(tabId);
      }
    },
    [closeTab, isTabUnsaved, onCloseTabWithUnsaved]
  );

  const handleKeyDown = (e: React.KeyboardEvent, tabId: string) => {
    if (e.target !== e.currentTarget) return;
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      setActiveTab(tabId);
    } else if (e.key === 'Delete' || e.key === 'Backspace') {
      e.preventDefault();
      if (isTabUnsaved(tabId) && onCloseTabWithUnsaved) {
        onCloseTabWithUnsaved(tabId);
      } else {
        closeTab(tabId);
      }
    } else if (e.key === 'ArrowRight' || e.key === 'ArrowLeft') {
      e.preventDefault();
      const currentIndex = tabs.findIndex((tab) => tab.id === tabId);
      if (currentIndex === -1) return;

      let nextIndex: number;
      if (e.key === 'ArrowRight') {
        nextIndex = currentIndex + 1 >= tabs.length ? 0 : currentIndex + 1;
      } else {
        nextIndex = currentIndex - 1 < 0 ? tabs.length - 1 : currentIndex - 1;
      }

      const nextTab = tabs[nextIndex];
      if (nextTab) {
        setActiveTab(nextTab.id);
        tabRefs.current.get(nextTab.id)?.focus();
      }
    }
  };

  return (
    <I18nProps>
      <div
        className={cn(
          'flex items-end gap-0 bg-background border-b border-border overflow-x-auto overflow-y-hidden pt-3',
          className
        )}
        role="tablist"
        aria-label="Wiki page Tabs"
      >
        {tabs.map((tab) => {
          const isActive = activeTabId === tab.id;
          const unsaved = isTabUnsaved(tab.id);

          return (
            <div
              key={tab.id}
              ref={(element) => {
                if (element) {
                  tabRefs.current.set(tab.id, element);
                } else {
                  tabRefs.current.delete(tab.id);
                }
              }}
              id={`page-tab-${tab.id}`}
              role="tab"
              aria-selected={isActive}
              aria-controls={`page-tabpanel-${tab.id}`}
              tabIndex={isActive ? 0 : -1}
              onClick={() => handleTabClick(tab.id)}
              onKeyDown={(e) => handleKeyDown(e, tab.id)}
              className={cn(
                'group relative flex items-center gap-2 h-8 px-3 min-w-[120px] max-w-[200px]',
                'border-b-2 transition-all duration-150 cursor-pointer shrink-0',
                'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-1',
                isActive
                  ? 'border-primary bg-transparent text-foreground'
                  : 'border-transparent bg-transparent text-text-secondary hover:bg-muted hover:text-foreground'
              )}
            >
              {/* Unsaved indicator */}
              {unsaved && (
                <span className="h-1.5 w-1.5 rounded-full bg-amber-500 shrink-0" />
              )}

              {/* Tab Label */}
              <span className="flex-1 text-sm font-medium truncate select-none">
                {tab.title || tab.wikiPagePath || (
                  <I18nText text={'Untitled'} />
                )}
              </span>

              {/* Tab Actions */}
              <div
                className={cn(
                  'flex items-center gap-0.5 shrink-0',
                  'opacity-0 group-hover:opacity-100',
                  isActive && 'opacity-100'
                )}
              >
                {/* More Menu */}
                <DropdownMenu>
                  <DropdownMenuTrigger asChild>
                    <button
                      type="button"
                      className="flex items-center justify-center w-4 h-4 rounded-sm hover:bg-muted-foreground/20"
                      onClick={(e) => e.stopPropagation()}
                      aria-label={ts('Actions for {name}', {
                        name: tab.title || tab.wikiPagePath,
                      })}
                      tabIndex={isActive ? 0 : -1}
                    >
                      <MoreHorizontal className="w-3 h-3" />
                    </button>
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align="start" className="w-44">
                    <DropdownMenuItem
                      onClick={(e) => {
                        e.stopPropagation();
                        if (isTabUnsaved(tab.id) && onCloseTabWithUnsaved) {
                          onCloseTabWithUnsaved(tab.id);
                        } else {
                          closeTab(tab.id);
                        }
                      }}
                    >
                      <X className="h-3.5 w-3.5 mr-2" />
                      <I18nText text={'Close'} />
                    </DropdownMenuItem>
                    <DropdownMenuItem
                      disabled={tabs.length <= 1}
                      onClick={(e) => {
                        e.stopPropagation();
                        onCloseOtherTabs?.(tab.id);
                      }}
                    >
                      <XCircle className="h-3.5 w-3.5 mr-2" />
                      <I18nText text={'Close Others'} />
                    </DropdownMenuItem>
                    <DropdownMenuItem
                      onClick={(e) => {
                        e.stopPropagation();
                        onCloseAllTabs?.();
                      }}
                    >
                      <XCircle className="h-3.5 w-3.5 mr-2" />
                      <I18nText text={'Close All'} />
                    </DropdownMenuItem>
                    {onDeleteWikiPage && (
                      <>
                        <DropdownMenuSeparator />
                        <DropdownMenuItem
                          className="text-destructive focus:text-destructive"
                          onClick={(e) => {
                            e.stopPropagation();
                            onDeleteWikiPage(
                              tab.wikiPagePath,
                              tab.title,
                              tab.workspace
                            );
                          }}
                        >
                          <Trash2 className="h-3.5 w-3.5 mr-2" />
                          <I18nText text={'Delete Wiki page'} />
                        </DropdownMenuItem>
                      </>
                    )}
                  </DropdownMenuContent>
                </DropdownMenu>

                {/* Close Button */}
                <button
                  type="button"
                  onClick={(e) => handleCloseTab(e, tab.id)}
                  className="flex items-center justify-center w-4 h-4 rounded-sm hover:bg-muted-foreground/20 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                  aria-label={ts('Close {name}', {
                    name: tab.title || tab.wikiPagePath,
                  })}
                  tabIndex={isActive ? 0 : -1}
                >
                  <X className="w-3 h-3" />
                </button>
              </div>
            </div>
          );
        })}
      </div>
    </I18nProps>
  );
}

export default WikiPageTabBar;

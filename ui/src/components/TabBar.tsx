import React from 'react';
import { X, Plus } from 'lucide-react';
import { useTabContext } from '@/contexts/TabContext';
import { cn } from '@/lib/utils';
import { I18nProps } from '@/i18n/I18nProps';
import { I18nText } from '@/i18n/I18nText';
import { useI18n } from '@/i18n/I18nProvider';

type TabBarProps = {
  className?: string;
  onAddTab?: () => void;
};

export function TabBar({ className, onAddTab }: TabBarProps) {
  const { ts } = useI18n();
  const { tabs, activeTabId, closeTab, setActiveTab } = useTabContext();

  const handleTabClick = (tabId: string) => {
    setActiveTab(tabId);
  };

  const handleCloseTab = (e: React.MouseEvent, tabId: string) => {
    e.stopPropagation();
    closeTab(tabId);
  };

  const handleAddTab = () => {
    if (onAddTab) {
      onAddTab();
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent, tabId: string) => {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      setActiveTab(tabId);
    } else if (e.key === 'Delete' || e.key === 'Backspace') {
      e.preventDefault();
      closeTab(tabId);
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
        aria-label="DAG Tabs"
      >
        {tabs.map((tab) => {
          const isActive = activeTabId === tab.id;

          return (
            <div
              key={tab.id}
              role="tab"
              aria-selected={isActive}
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
              {/* Tab Label */}
              <span className="flex-1 text-sm font-medium truncate select-none">
                {tab.title || tab.fileName || <I18nText text={'Untitled'} />}
              </span>

              {/* Close Button - Visible on hover or when active */}
              <button
                type="button"
                onClick={(e) => handleCloseTab(e, tab.id)}
                className={cn(
                  'flex items-center justify-center shrink-0 w-4 h-4 rounded-sm',
                  'transition-all duration-150',
                  'hover:bg-muted-foreground/20 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring',
                  'opacity-0 group-hover:opacity-100',
                  isActive && 'opacity-100'
                )}
                aria-label={ts('Close {name}', {
                  name: tab.title || tab.fileName,
                })}
              >
                <X className="w-3 h-3" />
              </button>
            </div>
          );
        })}

        {/* Add Tab Button */}
        <I18nProps>
          <button
            type="button"
            onClick={handleAddTab}
            className={cn(
              'flex items-center justify-center shrink-0 w-8 h-8 ml-1',
              'text-text-secondary hover:text-foreground hover:bg-muted',
              'transition-all duration-150 rounded-sm',
              'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'
            )}
            aria-label="Add new tab"
          >
            <Plus className="w-4 h-4" />
          </button>
        </I18nProps>
      </div>
    </I18nProps>
  );
}

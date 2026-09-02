// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useWikiPageTabContext } from '@/contexts/WikiPageTabContext';
import { FileText } from 'lucide-react';
import React, { useCallback, useState } from 'react';
import ConfirmModal from '@/components/ui/confirm-dialog';
import WikiPageEditor from './WikiPageEditor';
import WikiPageTabBar from './WikiPageTabBar';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';
import { I18nTemplate } from '@/i18n/I18nTemplate';

type Props = {
  onDeleteWikiPage?: (
    wikiPagePath: string,
    title: string,
    workspace?: string | null
  ) => void;
  toolbar?: React.ReactNode;
  onContentChange?: (content: string | null) => void;
};

function WikiPageTabEditorPanel({
  onDeleteWikiPage,
  toolbar,
  onContentChange,
}: Props) {
  const {
    tabs,
    activeTabId,
    closeTab,
    closeAllTabs,
    closeOtherTabs,
    clearDraft,
    markTabSaved,
    isTabUnsaved,
  } = useWikiPageTabContext();

  const [confirmCloseTabId, setConfirmCloseTabId] = useState<string | null>(
    null
  );

  const activeTab = activeTabId ? tabs.find((t) => t.id === activeTabId) : null;

  const handleCloseTabWithUnsaved = useCallback((tabId: string) => {
    setConfirmCloseTabId(tabId);
  }, []);

  const handleConfirmClose = useCallback(() => {
    if (confirmCloseTabId) {
      clearDraft(confirmCloseTabId);
      markTabSaved(confirmCloseTabId);
      closeTab(confirmCloseTabId);
      setConfirmCloseTabId(null);
    }
  }, [confirmCloseTabId, closeTab, clearDraft, markTabSaved]);

  // Close All Tabs
  const [confirmCloseAll, setConfirmCloseAll] = useState(false);

  const handleCloseAllTabs = useCallback(() => {
    if (tabs.some((t) => isTabUnsaved(t.id))) {
      setConfirmCloseAll(true);
    } else {
      closeAllTabs();
    }
  }, [tabs, isTabUnsaved, closeAllTabs]);

  const handleConfirmCloseAll = useCallback(() => {
    tabs.forEach((t) => {
      clearDraft(t.id);
      markTabSaved(t.id);
    });
    closeAllTabs();
    setConfirmCloseAll(false);
  }, [tabs, closeAllTabs, clearDraft, markTabSaved]);

  // Close Other Tabs
  const [confirmCloseOthersKeepId, setConfirmCloseOthersKeepId] = useState<
    string | null
  >(null);

  const handleCloseOtherTabs = useCallback(
    (keepTabId: string) => {
      if (tabs.some((t) => t.id !== keepTabId && isTabUnsaved(t.id))) {
        setConfirmCloseOthersKeepId(keepTabId);
      } else {
        closeOtherTabs(keepTabId);
      }
    },
    [tabs, isTabUnsaved, closeOtherTabs]
  );

  const handleConfirmCloseOthers = useCallback(() => {
    if (confirmCloseOthersKeepId) {
      tabs.forEach((t) => {
        if (t.id !== confirmCloseOthersKeepId) {
          clearDraft(t.id);
          markTabSaved(t.id);
        }
      });
      closeOtherTabs(confirmCloseOthersKeepId);
      setConfirmCloseOthersKeepId(null);
    }
  }, [
    confirmCloseOthersKeepId,
    tabs,
    closeOtherTabs,
    clearDraft,
    markTabSaved,
  ]);

  const confirmTab = confirmCloseTabId
    ? tabs.find((t) => t.id === confirmCloseTabId)
    : null;

  if (tabs.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center h-full gap-3">
        <FileText className="h-8 w-8 text-muted-foreground/50" />
        <p className="text-sm text-muted-foreground">
          <I18nText text={'Select a Wiki page to start editing.'} />
        </p>
      </div>
    );
  }

  return (
    <div className="flex flex-col h-full">
      <div className="flex items-stretch border-b border-border">
        {toolbar && (
          <div className="flex items-center shrink-0 px-2 gap-2">{toolbar}</div>
        )}
        <WikiPageTabBar
          className="flex-1 min-w-0 border-b-0"
          onCloseTabWithUnsaved={handleCloseTabWithUnsaved}
          onDeleteWikiPage={onDeleteWikiPage}
          onCloseAllTabs={handleCloseAllTabs}
          onCloseOtherTabs={handleCloseOtherTabs}
        />
      </div>
      <div className="flex-1 overflow-hidden min-h-0">
        {activeTab ? (
          <div
            key={activeTab.id}
            id={`page-tabpanel-${activeTab.id}`}
            role="tabpanel"
            aria-labelledby={`page-tab-${activeTab.id}`}
            className="h-full"
          >
            <WikiPageEditor
              tabId={activeTab.id}
              wikiPagePath={activeTab.wikiPagePath}
              workspace={activeTab.workspace}
              onDeleteWikiPage={
                onDeleteWikiPage
                  ? () =>
                      onDeleteWikiPage(
                        activeTab.wikiPagePath,
                        activeTab.title,
                        activeTab.workspace
                      )
                  : undefined
              }
              onContentChange={onContentChange}
            />
          </div>
        ) : (
          <div className="flex items-center justify-center h-full">
            <p className="text-sm text-muted-foreground">
              <I18nText text={'Select a tab to continue editing.'} />
            </p>
          </div>
        )}
      </div>

      {/* Confirm close unsaved tab */}
      <I18nProps>
        <ConfirmModal
          title="Unsaved Changes"
          buttonText="Discard"
          visible={!!confirmCloseTabId}
          dismissModal={() => setConfirmCloseTabId(null)}
          onSubmit={handleConfirmClose}
        >
          <p className="text-sm text-muted-foreground">
            <I18nTemplate
              text="You have unsaved changes in {page}. Discard changes?"
              values={{
                page: (
                  <strong>
                    {confirmTab?.title || confirmTab?.wikiPagePath || (
                      <I18nText text="this Wiki page" />
                    )}
                  </strong>
                ),
              }}
            />
          </p>
        </ConfirmModal>
      </I18nProps>

      {/* Confirm close all tabs */}
      <I18nProps>
        <ConfirmModal
          title="Close All Tabs"
          buttonText="Discard & Close All"
          visible={confirmCloseAll}
          dismissModal={() => setConfirmCloseAll(false)}
          onSubmit={handleConfirmCloseAll}
        >
          <p className="text-sm text-muted-foreground">
            <I18nText
              text={
                'Some tabs have unsaved changes. Discard all changes and close all tabs?'
              }
            />
          </p>
        </ConfirmModal>
      </I18nProps>

      {/* Confirm close other tabs */}
      <I18nProps>
        <ConfirmModal
          title="Close Other Tabs"
          buttonText="Discard & Close Others"
          visible={!!confirmCloseOthersKeepId}
          dismissModal={() => setConfirmCloseOthersKeepId(null)}
          onSubmit={handleConfirmCloseOthers}
        >
          <p className="text-sm text-muted-foreground">
            <I18nText
              text={
                'Some other tabs have unsaved changes. Discard their changes and close them?'
              }
            />
          </p>
        </ConfirmModal>
      </I18nProps>
    </div>
  );
}

export default WikiPageTabEditorPanel;

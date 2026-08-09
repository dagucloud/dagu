// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Plus, X } from 'lucide-react';
import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  useWikiPageTemplates,
  useResolveTemplateContent,
} from '../hooks/useWikiPageTemplates';
import { validateWikiPagePath } from '../lib/wiki-page-validation';

const BLANK_TEMPLATE_ID = 'blank';

interface CreateWikiPageModalProps {
  isOpen: boolean;
  onClose: () => void;
  onSubmit: (path: string, content: string) => Promise<void>;
  parentDir?: string;
  workspace?: string | null;
  isLoading?: boolean;
  externalError?: string | null;
}

export function CreateWikiPageModal({
  isOpen,
  onClose,
  onSubmit,
  parentDir = '',
  workspace = null,
  isLoading = false,
  externalError = null,
}: CreateWikiPageModalProps) {
  const [path, setPath] = useState('');
  const [templateId, setTemplateId] = useState(BLANK_TEMPLATE_ID);
  const [validationError, setValidationError] = useState<string | null>(null);
  const [templateError, setTemplateError] = useState<string | null>(null);
  const [resolvingTemplate, setResolvingTemplate] = useState(false);

  const templates = useWikiPageTemplates(isOpen, workspace);
  const resolveTemplateContent = useResolveTemplateContent();
  const selectedTemplate = useMemo(
    () => templates.find((t) => t.id === templateId) ?? null,
    [templates, templateId]
  );

  useEffect(() => {
    if (isOpen) {
      setPath(parentDir ? `${parentDir}/` : '');
      setTemplateId(BLANK_TEMPLATE_ID);
      setValidationError(null);
      setTemplateError(null);
    }
  }, [isOpen, parentDir]);

  const handlePathChange = useCallback(
    (e: React.ChangeEvent<HTMLInputElement>) => {
      setPath(e.target.value);
      if (validationError) {
        setValidationError(null);
      }
    },
    [validationError]
  );

  const handleSubmit = useCallback(
    async (e: React.FormEvent) => {
      e.preventDefault();
      const trimmed = path.trim();
      const validation = validateWikiPagePath(trimmed);
      if (!validation.isValid) {
        setValidationError(validation.error || 'Invalid path');
        return;
      }
      let content = '';
      if (selectedTemplate) {
        setResolvingTemplate(true);
        setTemplateError(null);
        try {
          content = await resolveTemplateContent(selectedTemplate);
        } catch (err) {
          setTemplateError(
            err instanceof Error ? err.message : 'Failed to load template'
          );
          return;
        } finally {
          setResolvingTemplate(false);
        }
      }
      await onSubmit(trimmed, content);
    },
    [path, selectedTemplate, resolveTemplateContent, onSubmit]
  );

  const currentError = validationError || templateError || externalError;
  const busy = isLoading || resolvingTemplate;

  return (
    <Dialog open={isOpen} onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-[425px]">
        <form onSubmit={handleSubmit}>
          <DialogHeader>
            <DialogTitle>Create New Wiki page</DialogTitle>
            <DialogDescription>
              Enter a path for the new Wiki page. Use / for directories.
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-3 py-3">
            <div className="grid grid-cols-4 items-center gap-3">
              <Label htmlFor="wiki-page-path" className="text-right">
                Path
              </Label>
              <Input
                id="wiki-page-path"
                value={path}
                onChange={handlePathChange}
                className="col-span-3 font-mono"
                placeholder="runbooks/deployment"
                autoFocus
                disabled={busy}
              />
            </div>
            <div className="grid grid-cols-4 items-center gap-3">
              <Label htmlFor="page-template" className="text-right">
                Template
              </Label>
              <Select
                value={templateId}
                onValueChange={setTemplateId}
                disabled={busy}
              >
                <SelectTrigger
                  id="page-template"
                  size="sm"
                  className="col-span-3 h-7"
                >
                  <SelectValue placeholder="Blank" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={BLANK_TEMPLATE_ID}>Blank</SelectItem>
                  {templates.map((template) => (
                    <SelectItem key={template.id} value={template.id}>
                      {template.name}
                      {!template.builtIn && ' (custom)'}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            {selectedTemplate?.description && (
              <div className="text-xs text-muted-foreground px-4">
                {selectedTemplate.description}
              </div>
            )}
            <div className="text-xs text-muted-foreground px-4">
              Relative path without .md extension. Use / for directories.
            </div>
            {currentError && (
              <div className="text-destructive text-sm px-4">
                {currentError}
              </div>
            )}
          </div>
          <DialogFooter>
            <Button
              type="button"
              variant="ghost"
              onClick={onClose}
              disabled={busy}
            >
              <X className="h-4 w-4" />
              Cancel
            </Button>
            <Button type="submit" disabled={busy}>
              <Plus className="h-4 w-4" />
              {busy ? 'Creating...' : 'Create'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

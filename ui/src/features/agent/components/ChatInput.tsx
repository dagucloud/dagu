import {
  useState,
  useCallback,
  useEffect,
  useRef,
  KeyboardEvent,
  ChangeEvent,
} from 'react';
import { Send, Square } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Textarea } from '@/components/ui/textarea';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { cn } from '@/lib/utils';
import { DAGContext } from '../types';
import { DAGPicker } from './DAGPicker';
import { DocPicker, type DocRef, type DocPickerHandle } from './DocPicker';
import { useDagPageContext } from '../hooks/useDagPageContext';
import { useDocPageContext } from '../hooks/useDocPageContext';
import { useAvailableModels } from '../hooks/useAvailableModels';
import { useAvailableSouls } from '../hooks/useAvailableSouls';

interface ChatInputProps {
  onSend: (
    message: string,
    dagContexts?: DAGContext[],
    model?: string,
    soulId?: string
  ) => void;
  onCancel?: () => void;
  isWorking: boolean;
  disabled?: boolean;
  placeholder?: string;
  initialValue?: string | null;
  hasActiveSession?: boolean;
}

export function ChatInput({
  onSend,
  onCancel,
  isWorking,
  disabled,
  placeholder = 'Type a message...',
  initialValue,
  hasActiveSession,
}: ChatInputProps) {
  const [message, setMessage] = useState('');
  const [isPending, setIsPending] = useState(false);
  const [selectedDags, setSelectedDags] = useState<DAGContext[]>([]);
  const { models, selectedModel, setSelectedModel } = useAvailableModels();
  const { souls, selectedSoul, setSelectedSoul } = useAvailableSouls();
  const currentPageDag = useDagPageContext();
  const currentPageDoc = useDocPageContext();
  // Track IME composition state manually for reliable Japanese/Chinese input handling
  const isComposingRef = useRef(false);

  // Doc picker state
  const [selectedDocs, setSelectedDocs] = useState<DocRef[]>([]);
  const [atMenuOpen, setAtMenuOpen] = useState(false);
  const [atQuery, setAtQuery] = useState('');
  const [atStart, setAtStart] = useState(-1);
  const docPickerRef = useRef<DocPickerHandle>(null);

  const textareaRef = useRef<HTMLTextAreaElement>(null);

  const showPauseButton = isPending || isWorking;

  // Pre-fill textarea with initial value (e.g., from setup wizard)
  useEffect(() => {
    if (initialValue) {
      setMessage(initialValue);
      requestAnimationFrame(() => {
        const el = textareaRef.current;
        if (el) {
          el.style.height = 'auto';
          el.style.height = `${Math.min(el.scrollHeight, 120)}px`;
        }
      });
    }
  }, [initialValue]);

  // Reset pending state when server confirms processing or after timeout fallback
  useEffect(() => {
    if (isWorking) {
      setIsPending(false);
      return;
    }
    if (isPending) {
      // Use longer timeout to ensure SSE has time to confirm working state
      const timer = setTimeout(() => setIsPending(false), 3000);
      return () => clearTimeout(timer);
    }
  }, [isWorking, isPending]);

  const handleSend = useCallback(() => {
    const trimmed = message.trim();
    // Allow sending while working (isWorking=true) - message will be queued
    // Only block during brief isPending state or when disabled
    if (!trimmed || isPending || disabled) {
      return;
    }

    setIsPending(true);

    // Build contexts: current page DAG first, then additional selected DAGs (excluding duplicates)
    const additionalDags = selectedDags.filter(
      (dag) => dag.dag_file !== currentPageDag?.dag_file
    );
    const allContexts = currentPageDag
      ? [currentPageDag, ...additionalDags]
      : additionalDags;

    // Prepend doc instructions if selected.
    let finalMessage = trimmed;
    const additionalDocs = selectedDocs.filter(
      (doc) => doc.id !== currentPageDoc?.id
    );
    const allDocs = currentPageDoc
      ? [currentPageDoc, ...additionalDocs]
      : additionalDocs;
    if (allDocs.length > 0) {
      const docPrefix = allDocs
        .map((d) => `[Doc: ${d.id} | ${d.title}]`)
        .join(' ');
      finalMessage = `${docPrefix}\n${finalMessage}`;
    }

    const soulValue =
      selectedSoul && selectedSoul !== '__default__' ? selectedSoul : undefined;
    onSend(
      finalMessage,
      allContexts.length > 0 ? allContexts : undefined,
      selectedModel || undefined,
      soulValue
    );
    setMessage('');
    setSelectedDocs([]);
  }, [
    message,
    isPending,
    disabled,
    onSend,
    selectedDags,
    currentPageDag,
    selectedModel,
    selectedDocs,
    currentPageDoc,
    selectedSoul,
  ]);

  const handleChange = useCallback(
    (e: ChangeEvent<HTMLTextAreaElement>) => {
      const val = e.target.value;
      const pos = e.target.selectionStart ?? val.length;
      setMessage(val);

      // Detect '@' at start or after whitespace to open doc menu.
      if (pos > 0 && val[pos - 1] === '@') {
        const charBefore = pos > 1 ? val[pos - 2] : undefined;
        if (
          charBefore === undefined ||
          charBefore === ' ' ||
          charBefore === '\n'
        ) {
          setAtStart(pos - 1);
          setAtMenuOpen(true);
          setAtQuery('');
          return;
        }
      }

      // Update filter query while at menu is open.
      if (atMenuOpen && atStart >= 0) {
        const query = val.substring(atStart + 1, pos);
        if (query.includes(' ')) {
          setAtMenuOpen(false);
          setAtStart(-1);
        } else {
          setAtQuery(query);
        }
      }
    },
    [atMenuOpen, atStart]
  );

  const handleDocSelect = useCallback(
    (doc: DocRef) => {
      if (!selectedDocs.find((d) => d.id === doc.id)) {
        setSelectedDocs((prev) => [...prev, doc]);
      }
      const before = message.substring(0, atStart);
      const after = message.substring(atStart + 1 + atQuery.length);
      setMessage(before + after);
      setAtMenuOpen(false);
      setAtStart(-1);
      setAtQuery('');
      textareaRef.current?.focus();
    },
    [selectedDocs, message, atStart, atQuery]
  );

  const handleDocRemove = useCallback((id: string) => {
    setSelectedDocs((prev) => prev.filter((d) => d.id !== id));
  }, []);

  const handleKeyDown = useCallback(
    (e: KeyboardEvent<HTMLTextAreaElement>) => {
      // Let doc picker handle keys when open.
      if (atMenuOpen && docPickerRef.current) {
        if (e.key === 'Backspace' && atQuery === '') {
          setAtMenuOpen(false);
          setAtStart(-1);
          return;
        }
        const consumed = docPickerRef.current.handleKeyDown(e);
        if (consumed) return;
      }

      // Ignore Enter during IME composition (e.g., Japanese input conversion)
      // Check both isComposing and our manual ref for cross-browser compatibility
      if (
        e.key === 'Enter' &&
        !e.shiftKey &&
        !e.nativeEvent.isComposing &&
        !isComposingRef.current
      ) {
        e.preventDefault();
        handleSend();
      }
    },
    [handleSend, atMenuOpen, atQuery]
  );

  const handleCompositionStart = useCallback(() => {
    isComposingRef.current = true;
  }, []);

  const handleCompositionEnd = useCallback(() => {
    isComposingRef.current = false;
  }, []);

  return (
    <div className="relative shrink-0 border-t border-border bg-card p-3">
      {/* DAG Picker with chips */}
      <DAGPicker
        selectedDags={selectedDags}
        onChange={setSelectedDags}
        currentPageDag={currentPageDag}
        disabled={disabled || showPauseButton}
      />

      {/* Doc chips and dropdown */}
      <DocPicker
        ref={docPickerRef}
        selectedDocs={selectedDocs}
        onSelect={handleDocSelect}
        onRemove={handleDocRemove}
        isOpen={atMenuOpen}
        onClose={() => {
          setAtMenuOpen(false);
          setAtStart(-1);
          setAtQuery('');
        }}
        filterQuery={atQuery}
        currentPageDoc={currentPageDoc}
        disabled={disabled || showPauseButton}
      />

      {/* Model & soul selector row */}
      {(models.length > 0 || (souls.length > 0 && !hasActiveSession)) && (
        <div className="mb-2 flex items-center gap-2">
          {models.length > 0 && (
            <Select value={selectedModel} onValueChange={setSelectedModel}>
              <SelectTrigger className="h-7 text-xs w-auto min-w-[140px] max-w-[200px]">
                <SelectValue placeholder="Select model" />
              </SelectTrigger>
              <SelectContent>
                {models.map((m) => (
                  <SelectItem key={m.id} value={m.id} className="text-xs">
                    {m.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
          {souls.length > 0 && !hasActiveSession && (
            <Select value={selectedSoul} onValueChange={setSelectedSoul}>
              <SelectTrigger className="h-7 text-xs w-auto min-w-[140px] max-w-[200px]">
                <SelectValue placeholder="default" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="__default__" className="text-xs">
                  default
                </SelectItem>
                {souls.map((s) => (
                  <SelectItem key={s.id} value={s.id} className="text-xs">
                    {s.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
        </div>
      )}

      {/* Input row */}
      <div className="flex items-end gap-2">
        <Textarea
          ref={textareaRef}
          autoFocus
          aria-label="Message agent"
          value={message}
          onChange={handleChange}
          onKeyDown={handleKeyDown}
          onCompositionStart={handleCompositionStart}
          onCompositionEnd={handleCompositionEnd}
          placeholder={placeholder}
          disabled={disabled}
          rows={1}
          className={cn(
            'max-h-[120px] min-h-[40px] flex-1 overflow-y-auto bg-background py-1 shadow-none',
            disabled && 'cursor-not-allowed opacity-50'
          )}
          style={{
            height: 'auto',
            minHeight: '40px',
          }}
          onInput={(e) => {
            const target = e.target as HTMLTextAreaElement;
            target.style.height = 'auto';
            target.style.height = `${Math.min(target.scrollHeight, 120)}px`;
          }}
        />
        {showPauseButton && (
          <Button
            size="icon"
            variant="destructive"
            onClick={onCancel}
            className="h-10 w-10"
            title="Stop"
            aria-label="Stop agent"
          >
            <Square className="h-4 w-4" />
          </Button>
        )}
        <Button
          size="icon"
          variant="primary"
          onClick={handleSend}
          disabled={!message.trim() || disabled || isPending}
          className="h-10 w-10"
          title="Send"
          aria-label="Send message"
        >
          <Send className="h-4 w-4" />
        </Button>
      </div>
    </div>
  );
}

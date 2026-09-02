import { Button } from '@/components/ui/button';
import { ExternalLink, Maximize2, Minimize2, X } from 'lucide-react';
import React, { useEffect, useState } from 'react';
import { I18nProps } from '@/i18n/I18nProps';

type LogSideModalProps = {
  isOpen: boolean;
  onClose: () => void;
  title: string;
  children: React.ReactNode;
  isInModal?: boolean;
  dagName?: string;
  dagRunId?: string;
  stepName?: string;
  logType?: 'execution' | 'step';
};

/**
 * LogSideModal displays log content in a side panel that appears from the right
 * This creates a developer-friendly experience for viewing logs without hiding the DAG details
 */
const LogSideModal: React.FC<LogSideModalProps> = ({
  isOpen,
  onClose,
  title,
  children,
  isInModal = false,
  dagName = '',
  dagRunId = '',
  stepName = '',
  logType = 'execution',
}) => {
  // State to track whether the modal is expanded
  const [isExpanded, setIsExpanded] = useState(false);

  // State to track if we're on mobile
  const [isMobile, setIsMobile] = useState(false);

  // Track if modal should be rendered and if it's visible (for animation)
  const [shouldRender, setShouldRender] = useState(isOpen);
  const [isVisible, setIsVisible] = useState(false);

  // Check if screen is mobile size
  useEffect(() => {
    const checkIsMobile = () => {
      setIsMobile(window.innerWidth < 768); // md breakpoint
    };

    checkIsMobile();
    window.addEventListener('resize', checkIsMobile);

    return () => {
      window.removeEventListener('resize', checkIsMobile);
    };
  }, []);

  // Handle open/close with animation
  useEffect(() => {
    if (isOpen) {
      // Start rendering, then trigger animation on next frame
      setShouldRender(true);
      requestAnimationFrame(() => {
        requestAnimationFrame(() => {
          setIsVisible(true);
        });
      });
    } else {
      // Start closing animation
      setIsVisible(false);
      // Remove from DOM after animation completes
      const timer = setTimeout(() => {
        setShouldRender(false);
      }, 150);
      return () => clearTimeout(timer);
    }
  }, [isOpen]);

  // Add keyboard shortcuts
  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      // Close modal with Escape key
      if (event.key === 'Escape') {
        onClose();
      }
    };

    if (isOpen) {
      window.addEventListener('keydown', handleKeyDown);
    }

    return () => {
      window.removeEventListener('keydown', handleKeyDown);
    };
  }, [isOpen, onClose]);

  if (!shouldRender) return null;

  // Calculate z-index and positioning based on whether it's in a modal or not
  const zIndex = isInModal ? 60 : 50; // Higher z-index when in modal

  // Determine width and positioning based on mobile/expanded state
  const width =
    isMobile || isExpanded ? 'w-full' : isInModal ? 'w-1/2' : 'w-2/5';
  const positioning = isMobile ? 'inset-0' : 'top-0 bottom-0 right-0';

  // Handle clicks outside the modal
  const handleOutsideClick = (e: React.MouseEvent) => {
    // Stop propagation to prevent closing the parent modal
    e.stopPropagation();
    onClose();
  };

  // Toggle expanded state
  const toggleExpand = () => {
    setIsExpanded(!isExpanded);
  };

  // Generate URL for opening log in new tab
  const getLogUrl = () => {
    const baseUrl = `/dags/${dagName}`;
    const searchParams = new URLSearchParams();

    if (dagRunId) {
      searchParams.set('dagRunId', dagRunId);
    }

    if (logType === 'step' && stepName) {
      searchParams.set('step', stepName);
      return `${baseUrl}/log?${searchParams.toString()}`;
    } else {
      return `${baseUrl}/dagRun-log?${searchParams.toString()}`;
    }
  };

  // Open log in new tab
  const openInNewTab = () => {
    window.open(getLogUrl(), '_blank');
  };

  return (
    <>
      {/* Invisible backdrop for capturing clicks outside the modal - always present */}
      <div
        className={`fixed inset-0 h-screen w-screen ${!isInModal || isMobile ? 'bg-black/50' : 'bg-transparent'}`}
        style={{ zIndex: zIndex }}
        onClick={isMobile ? onClose : handleOutsideClick}
      />

      {/* Modal - full screen on mobile, side panel on desktop */}
      <div
        className={`fixed ${positioning} ${width} h-screen bg-background ${
          isMobile
            ? 'border border-border rounded-none'
            : 'border-l border-border'
        } overflow-hidden flex flex-col transition-all duration-150 ease-out ${
          isMobile
            ? isVisible
              ? 'translate-y-0 opacity-100'
              : 'translate-y-full opacity-0'
            : isVisible
              ? 'translate-x-0 opacity-100'
              : 'translate-x-full opacity-0'
        }`}
        style={{ zIndex: zIndex + 1 }} // Make sure modal is above backdrop
        onClick={(e) => e.stopPropagation()} // Prevent clicks inside the modal from closing it
      >
        <div
          className={`flex justify-between items-center ${isMobile ? 'p-3' : 'p-4'} border-b`}
        >
          <h2 className={`${isMobile ? 'text-base' : 'text-lg'} font-semibold`}>
            {title}
          </h2>
          <div className="flex items-center gap-2">
            <div className="flex gap-1">
              {/* Hide expand/minimize button on mobile since it's always full screen */}
              {!isMobile && (
                <I18nProps>
                  <Button
                    size="icon"
                    onClick={toggleExpand}
                    title={isExpanded ? 'Minimize' : 'Expand'}
                  >
                    {isExpanded ? (
                      <Minimize2 className="h-4 w-4" />
                    ) : (
                      <Maximize2 className="h-4 w-4" />
                    )}
                  </Button>
                </I18nProps>
              )}
              <I18nProps>
                <Button
                  size="icon"
                  onClick={openInNewTab}
                  title="Open in new tab"
                >
                  <ExternalLink className="h-4 w-4" />
                </Button>
              </I18nProps>
              <I18nProps>
                <Button size="icon" onClick={onClose} title="Close (Esc)">
                  <X className="h-4 w-4" />
                </Button>
              </I18nProps>
            </div>
          </div>
        </div>
        <div className={`flex-1 overflow-auto ${isMobile ? 'p-2' : 'p-4'}`}>
          {children}
        </div>
      </div>
    </>
  );
};

export default LogSideModal;

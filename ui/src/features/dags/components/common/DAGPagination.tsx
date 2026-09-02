import { Button } from '@/components/ui/button'; // Import shadcn Button
import { Input } from '@/components/ui/input'; // Import shadcn Input
import {
  Pagination,
  PaginationContent,
  PaginationItem,
} from '@/components/ui/pagination'; // Import shadcn Pagination components
import React from 'react';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';

/**
 * Props for the DAGPagination component
 */
type DAGPaginationProps = {
  /** Total number of pages */
  totalPages: number;
  /** Current page number */
  page: number;
  /** Number of items per page */
  pageLimit: number;
  /** Callback for page change */
  pageChange: (page: number) => void;
  /** Callback for page limit change */
  onPageLimitChange?: (pageLimit: number) => void;
  /** Whether to show the page-size selector */
  showPageLimitSelector?: boolean;
};

/**
 * Helper function to generate pagination items with ellipsis
 */
const generatePaginationItems = (
  currentPage: number,
  totalPages: number,
  onPageChange: (page: number) => void
) => {
  const items = [];
  const isMobile = typeof window !== 'undefined' && window.innerWidth < 640;
  const maxPagesToShow = isMobile ? 3 : 5; // Show fewer pages on mobile
  const halfMaxPages = Math.floor(maxPagesToShow / 2);

  // Always show Previous button (icon only)
  items.push(
    <PaginationItem key="prev">
      <Button
        variant="ghost"
        size="icon"
        className={`h-6 w-6 sm:h-7 sm:w-7 rounded-md flex items-center justify-center cursor-pointer ${currentPage <= 1 ? 'pointer-events-none opacity-50' : 'text-muted-foreground hover:bg-muted hover:text-foreground'} transition-colors`}
        disabled={currentPage <= 1}
        onClick={(e) => {
          e.preventDefault();
          if (currentPage > 1) onPageChange(currentPage - 1);
        }}
      >
        <svg
          xmlns="http://www.w3.org/2000/svg"
          width="14"
          height="14"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
        >
          <path d="m15 18-6-6 6-6" />
        </svg>
        <span className="sr-only">
          <I18nText text={'Previous page'} />
        </span>
      </Button>
    </PaginationItem>
  );

  // Logic to show page numbers and ellipsis
  if (totalPages <= maxPagesToShow + 2) {
    // Show all pages if not too many
    for (let i = 1; i <= totalPages; i++) {
      items.push(
        <PaginationItem key={i}>
          <Button
            variant="ghost"
            size="icon"
            className={`h-6 w-6 sm:h-7 sm:w-7 rounded-md text-xs flex items-center justify-center hover:bg-muted transition-colors cursor-pointer ${i === currentPage ? 'bg-primary/15 hover:bg-primary/20 text-primary font-medium' : 'text-muted-foreground font-normal'}`}
            onClick={(e) => {
              e.preventDefault();
              onPageChange(i);
            }}
          >
            {i}
            <span className="sr-only">
              <I18nText text="Page {page}" values={{ page: i }} />
            </span>
          </Button>
        </PaginationItem>
      );
    }
  } else {
    // Show first page
    items.push(
      <PaginationItem key={1}>
        <Button
          variant="ghost"
          size="icon"
          className={`h-6 w-6 rounded-md text-xs flex items-center justify-center hover:bg-muted transition-colors cursor-pointer ${1 === currentPage ? 'bg-primary/15 hover:bg-primary/20 text-primary font-medium' : 'text-muted-foreground font-normal'}`}
          onClick={(e) => {
            e.preventDefault();
            onPageChange(1);
          }}
        >
          1
          <span className="sr-only">
            <I18nText text={'Page 1'} />
          </span>
        </Button>
      </PaginationItem>
    );

    // Ellipsis after first page?
    if (currentPage > halfMaxPages + 2) {
      items.push(
        <PaginationItem key="ellipsis-start">
          <div className="h-6 w-6 sm:h-7 sm:w-7 flex items-center justify-center text-muted-foreground">
            <span className="text-xs">•••</span>
          </div>
        </PaginationItem>
      );
    }

    // Middle pages
    const startPage = Math.max(2, currentPage - halfMaxPages);
    const endPage = Math.min(totalPages - 1, currentPage + halfMaxPages);

    for (let i = startPage; i <= endPage; i++) {
      items.push(
        <PaginationItem key={i}>
          <Button
            variant="ghost"
            size="icon"
            className={`h-6 w-6 sm:h-7 sm:w-7 rounded-md text-xs flex items-center justify-center hover:bg-muted transition-colors cursor-pointer ${i === currentPage ? 'bg-primary/15 hover:bg-primary/20 text-primary font-medium' : 'text-muted-foreground font-normal'}`}
            onClick={(e) => {
              e.preventDefault();
              onPageChange(i);
            }}
          >
            {i}
            <span className="sr-only">
              <I18nText text="Page {page}" values={{ page: i }} />
            </span>
          </Button>
        </PaginationItem>
      );
    }

    // Ellipsis before last page?
    if (currentPage < totalPages - halfMaxPages - 1) {
      items.push(
        <PaginationItem key="ellipsis-end">
          <div className="h-6 w-6 sm:h-7 sm:w-7 flex items-center justify-center text-muted-foreground">
            <span className="text-xs">•••</span>
          </div>
        </PaginationItem>
      );
    }

    // Show last page
    items.push(
      <PaginationItem key={totalPages}>
        <Button
          variant="ghost"
          size="icon"
          className={`h-6 w-6 rounded-md text-xs flex items-center justify-center hover:bg-muted transition-colors cursor-pointer ${totalPages === currentPage ? 'bg-primary/15 hover:bg-primary/20 text-primary font-medium' : 'text-muted-foreground font-normal'}`}
          onClick={(e) => {
            e.preventDefault();
            onPageChange(totalPages);
          }}
        >
          {totalPages}
          <span className="sr-only">
            <I18nText text="Page {page}" values={{ page: totalPages }} />
          </span>
        </Button>
      </PaginationItem>
    );
  }

  // Always show Next button (icon only)
  items.push(
    <PaginationItem key="next">
      <Button
        variant="ghost"
        size="icon"
        className={`h-6 w-6 sm:h-7 sm:w-7 rounded-md flex items-center justify-center cursor-pointer ${currentPage >= totalPages ? 'pointer-events-none opacity-50' : 'text-muted-foreground hover:bg-muted hover:text-foreground'} transition-colors`}
        disabled={currentPage >= totalPages}
        onClick={(e) => {
          e.preventDefault();
          if (currentPage < totalPages) onPageChange(currentPage + 1);
        }}
      >
        <svg
          xmlns="http://www.w3.org/2000/svg"
          width="14"
          height="14"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
        >
          <path d="m9 18 6-6-6-6" />
        </svg>
        <span className="sr-only">
          <I18nText text={'Next page'} />
        </span>
      </Button>
    </PaginationItem>
  );

  return items;
};

/**
 * DAGPagination provides pagination controls with customizable page size
 */
const DAGPagination = ({
  totalPages,
  page,
  pageChange,
  pageLimit,
  onPageLimitChange,
  showPageLimitSelector = !!onPageLimitChange,
}: DAGPaginationProps) => {
  // State for the input field value
  const [inputValue, setInputValue] = React.useState(pageLimit.toString());

  // Update input value when pageLimit prop changes
  React.useEffect(() => {
    setInputValue(pageLimit.toString());
  }, [pageLimit]);

  /**
   * Handle input change for page limit
   */
  const handleLimitChange = (event: React.ChangeEvent<HTMLInputElement>) => {
    const value = event.target.value;
    setInputValue(value);
  };

  /**
   * Apply the page limit change
   */
  const commitChange = () => {
    const numValue = parseInt(inputValue);
    if (!isNaN(numValue) && numValue > 0 && onPageLimitChange) {
      onPageLimitChange(numValue);
    } else {
      // Reset to current page limit if invalid input
      setInputValue(pageLimit.toString());
    }
  };

  /**
   * Handle Enter key press to commit changes
   */
  const handleKeyDown = (event: React.KeyboardEvent<HTMLInputElement>) => {
    if (event.key === 'Enter') {
      commitChange();
      event.preventDefault();
      (event.target as HTMLInputElement).blur(); // Remove focus after Enter
    }
  };

  return (
    <div className="flex items-center gap-1 sm:gap-2">
      {/* Pagination controls */}
      <Pagination>
        <PaginationContent className="flex items-center space-x-0.5 sm:space-x-1">
          {generatePaginationItems(page, totalPages, pageChange)}
        </PaginationContent>
      </Pagination>

      {/* Items per page selector - hidden on very small screens */}
      {showPageLimitSelector && onPageLimitChange && (
        <div className="hidden sm:flex items-center gap-1">
          <span className="text-xs text-muted-foreground">{pageLimit}</span>
          <div className="relative group">
            <Button
              variant="ghost"
              size="icon"
              className="h-6 w-6 rounded-md hover:bg-muted flex items-center justify-center cursor-pointer"
            >
              <svg
                xmlns="http://www.w3.org/2000/svg"
                width="12"
                height="12"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
                strokeLinecap="round"
                strokeLinejoin="round"
                className="text-muted-foreground"
              >
                <circle cx="12" cy="12" r="1" />
                <circle cx="12" cy="5" r="1" />
                <circle cx="12" cy="19" r="1" />
              </svg>
            </Button>
            <div className="absolute right-0 mt-1 w-[100px] bg-background border border-border rounded-md opacity-0 invisible group-hover:opacity-100 group-hover:visible transition-all duration-200 z-10">
              {[10, 25, 50, 100, 200].map((limit) => (
                <div
                  key={limit}
                  className={`px-2 py-1 text-xs cursor-pointer hover:bg-muted transition-colors ${pageLimit === limit ? 'bg-primary/15 text-primary font-medium' : ''}`}
                  onClick={() => onPageLimitChange(limit)}
                >
                  {limit}
                </div>
              ))}
              <div className="px-2 py-1 border-t border-border">
                <I18nProps>
                  <Input
                    type="number"
                    min="1"
                    className="h-6 text-xs"
                    value={inputValue}
                    onChange={handleLimitChange}
                    onBlur={commitChange}
                    onKeyDown={handleKeyDown}
                    placeholder="Custom"
                  />
                </I18nProps>
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  );
};

export default DAGPagination;

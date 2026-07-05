import { Button } from "@fluentui/react-components";
import {
  ChevronLeft20Regular,
  ChevronRight20Regular,
} from "@fluentui/react-icons";
import styles from "./Pager.module.css";

export interface PagerProps {
  pageNumber: number;
  /**
   * Total page count for the active prefix, derived from
   * `CountVerticesByPrefix` (see `selectTotalPages`). When present the
   * indicator reads "Page N of M"; when `null`/`undefined` (no fresh count)
   * it falls back to the bare "Page N". Display-only — navigability stays
   * cursor-driven via `canGoNext`, since the count is approximate under
   * concurrent writes.
   */
  totalPages?: number | null;
  canGoPrevious: boolean;
  canGoNext: boolean;
  loading: boolean;
  onPrevious: () => void;
  onNext: () => void;
}

/**
 * Cursor-paginated Prev/Next chrome. Deliberately local to the
 * browse-vertices feature; the browse-edges variant ships its own copy per
 * the F2 boundary rule ("Promote nothing to shared/ yet"). If a third
 * caller appears we can lift this then.
 */
export function Pager({
  pageNumber,
  totalPages,
  canGoPrevious,
  canGoNext,
  loading,
  onPrevious,
  onNext,
}: PagerProps) {
  return (
    <div className={styles.pager} role="navigation" aria-label="Pagination">
      <Button
        appearance="subtle"
        icon={<ChevronLeft20Regular />}
        onClick={onPrevious}
        disabled={!canGoPrevious || loading}
        data-testid="pager-previous"
      >
        Previous
      </Button>
      <span className={styles.indicator} data-testid="pager-page">
        {totalPages != null
          ? `Page ${pageNumber} of ${totalPages}`
          : `Page ${pageNumber}`}
      </span>
      <Button
        appearance="subtle"
        iconPosition="after"
        icon={<ChevronRight20Regular />}
        onClick={onNext}
        disabled={!canGoNext || loading}
        data-testid="pager-next"
      >
        Next
      </Button>
    </div>
  );
}

import {
  Button,
  Menu,
  MenuItem,
  MenuList,
  MenuPopover,
  MenuTrigger,
  Overflow,
  OverflowItem,
  Tooltip,
  useIsOverflowItemVisible,
  useOverflowMenu,
} from "@fluentui/react-components";
import { MoreHorizontal20Regular, Star20Filled } from "@fluentui/react-icons";
import type { ExpansionChip } from "~/lib/client/usecase/illuminate/selectors";
import styles from "./ExpansionChipStrip.module.css";

export interface ExpansionChipStripProps {
  chips: ExpansionChip[];
  /**
   * Pure-UI scroll-to-origin handler. Should pan the canvas to centre
   * the supplied vertex and highlight it for ~600 ms. The chip strip
   * never mutates `IlluminateState` itself — by contract this callback
   * must not re-fetch or push a new expansion (per #456 acceptance
   * criteria "does not mutate state, does not re-trigger any RPC").
   */
  onChipClick: (originKey: string) => void;
}

/**
 * Horizontal lineage strip showing one chip per recorded expansion
 * (#456). Click a chip to scroll the Sigma camera back to that
 * expansion's origin vertex. Renders nothing when there are no
 * expansions yet — the `SeedPrompt` covers that case at the page level.
 *
 * Overflow handling: Fluent UI's `<Overflow>` watches the container
 * width and hides chips that don't fit, surfacing the hidden tail
 * through an overflow menu (`<MenuButton>` with `<MoreHorizontal20Regular>`).
 * The seed chip (`chips[0]`) is given `priority={1}` so it stays visible
 * even on narrow viewports — losing the seed marker would erase the
 * #466 D5 "structurally privileged" entry point from the picture.
 */
export function ExpansionChipStrip({
  chips,
  onChipClick,
}: ExpansionChipStripProps) {
  if (chips.length === 0) return null;

  return (
    <div
      className={styles.strip}
      role="toolbar"
      aria-label="Expansion lineage"
      data-testid="illuminate-expansion-chips"
    >
      <Overflow minimumVisible={1}>
        <div className={styles.overflowContainer}>
          {chips.map((chip) => (
            <OverflowItem
              key={chip.id}
              id={`expansion-chip-${chip.id}`}
              priority={chip.isSeed ? 1 : 0}
            >
              <ChipButton chip={chip} onChipClick={onChipClick} />
            </OverflowItem>
          ))}
          <OverflowMenu chips={chips} onChipClick={onChipClick} />
        </div>
      </Overflow>
    </div>
  );
}

interface ChipButtonProps {
  chip: ExpansionChip;
  onChipClick: (originKey: string) => void;
}

function ChipButton({ chip, onChipClick }: ChipButtonProps) {
  const tooltipContent = chip.isSeed
    ? `Seed: ${chip.originKey} — pan camera to the initial seed`
    : `Expansion #${chip.index + 1}: ${chip.originKey} — pan camera to this expansion`;

  return (
    <Tooltip content={tooltipContent} relationship="label" withArrow>
      <Button
        appearance="subtle"
        size="small"
        icon={chip.isSeed ? <Star20Filled /> : undefined}
        onClick={() => onChipClick(chip.originKey)}
        data-testid={`illuminate-chip-${chip.index}`}
        data-chip-origin={chip.originKey}
        data-chip-is-seed={chip.isSeed ? "true" : "false"}
        className={styles.chip}
      >
        {chip.originKey}
      </Button>
    </Tooltip>
  );
}

interface OverflowMenuProps {
  chips: ExpansionChip[];
  onChipClick: (originKey: string) => void;
}

/**
 * Renders an overflow `<MenuButton>` populated with the chips that the
 * `<Overflow>` container hid. We can't put `useIsOverflowItemVisible`
 * inside the parent's `chips.map` callback — it would call the hook in
 * a loop and break React's rules-of-hooks. Instead the menu maps over
 * `chips` itself and asks per-id whether the item is hidden.
 */
function OverflowMenu({ chips, onChipClick }: OverflowMenuProps) {
  const { ref, isOverflowing, overflowCount } =
    useOverflowMenu<HTMLButtonElement>();

  if (!isOverflowing) return null;

  return (
    <Menu>
      <MenuTrigger disableButtonEnhancement>
        <Tooltip
          content={`Show ${overflowCount} more expansion${overflowCount === 1 ? "" : "s"}`}
          relationship="label"
          withArrow
        >
          <Button
            ref={ref}
            appearance="subtle"
            size="small"
            icon={<MoreHorizontal20Regular />}
            data-testid="illuminate-chip-overflow"
            aria-label={`Show ${overflowCount} more expansions`}
          />
        </Tooltip>
      </MenuTrigger>
      <MenuPopover>
        <MenuList>
          {chips.map((chip) => (
            <OverflowMenuItem
              key={chip.id}
              chip={chip}
              onChipClick={onChipClick}
            />
          ))}
        </MenuList>
      </MenuPopover>
    </Menu>
  );
}

interface OverflowMenuItemProps {
  chip: ExpansionChip;
  onChipClick: (originKey: string) => void;
}

function OverflowMenuItem({ chip, onChipClick }: OverflowMenuItemProps) {
  const isVisible = useIsOverflowItemVisible(`expansion-chip-${chip.id}`);
  if (isVisible) return null;
  return (
    <MenuItem
      icon={chip.isSeed ? <Star20Filled /> : undefined}
      onClick={() => onChipClick(chip.originKey)}
      data-testid={`illuminate-chip-overflow-${chip.index}`}
    >
      {chip.isSeed ? `Seed · ${chip.originKey}` : chip.originKey}
    </MenuItem>
  );
}

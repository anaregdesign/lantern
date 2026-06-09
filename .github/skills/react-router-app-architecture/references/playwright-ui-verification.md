# Playwright UI Verification

## Goal

Check the real rendered UI before pushing when a change affects presentation,
interaction, or responsive behavior.

## When To Use It

Run this workflow when the change touches any of the following:

- route UI or page layout
- component markup or styling
- interactive flows such as forms, dialogs, popovers, or steppers
- Tooltip or InfoLabel behavior
- charts, tables, empty states, error states, or loading states
- responsive layout, spacing, or typography

## Verification Workflow

1. Start the local app and confirm the route you need to inspect.
2. Open the route in Playwright instead of relying on static code reading.
3. Check the default happy path first, then the states most likely to
   regress:
   - loading
   - empty
   - error
   - success
   - disabled or permission-limited states when relevant
4. Resize to the viewports that matter for the change, usually at least one
   desktop width and one narrow mobile width when layout is affected. If
   orientation matters, check both portrait and landscape.
5. Exercise the interaction with realistic user input:
   - click
   - keyboard navigation
   - hover and focus
   - form entry and submission
6. Confirm that labels, focus treatment, validation, and critical status
   remain visible without depending only on hover Tooltip behavior.
7. Capture screenshots or snapshots when the visual result matters or when
   you need evidence for a follow-up fix.
8. Re-run the touched flow after the fix, not just the unit or type-level
   checks.

## Playwright Usage Rules

- Prefer accessible locators such as role, label, placeholder, and text-based
  intent over brittle CSS selectors.
- Prefer web-first assertions and visible UI state over fixed sleeps or
  arbitrary timeouts.
- Navigate through the real route and interaction flow rather than jumping
  straight to internal DOM details.
- When the UI includes charts or dense data displays, verify readability,
  legends, labels, and non-hover access to the key insight.
- When the UI includes Tooltip, Popover, or InfoLabel patterns, verify both
  hover and keyboard focus behavior.
- When the UI is mobile-sensitive, verify there is no accidental horizontal
  scrolling, clipped call to action, or hidden validation at narrow widths.

## What To Inspect

- route title and primary heading
- primary call to action and destructive action states
- keyboard tab order and focus visibility
- validation messages and disabled states
- empty, error, and loading states
- responsive stacking, overflow, and truncation
- touch-sized layouts, bottom bars, and dialog or drawer behavior on narrow
  screens
- chart labels, legend clarity, summary text, and table fallback when
  applicable

## Tooling Notes

When Playwright browser tooling is available in the agent runtime:

- use browser navigation to open the local route
- use resize before judging responsive layout
- use accessibility snapshots to confirm structure and visible names
- use screenshots when the visual presentation itself is under review

Do not treat DOM inspection alone as sufficient UI verification for a
user-facing change.

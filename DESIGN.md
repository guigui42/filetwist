---
name: Filetwist
description: A home media service bench for compatibility-first photo, audio, and video conversion.
colors:
  bench: "#dce3e8"
  bench-deep: "#cbd4db"
  surface: "#f8f9f7"
  surface-raised: "#ffffff"
  surface-alt: "#e9eef1"
  surface-strong: "#d7dfe5"
  border: "#788592"
  border-soft: "#c8d0d6"
  text: "#111820"
  muted: "#52616f"
  accent: "#1769e0"
  accent-strong: "#0e4fae"
  accent-soft: "#e4efff"
  accent-text: "#ffffff"
  ok: "#14784f"
  ok-soft: "#e3f4eb"
  warn: "#7c5100"
  warn-soft: "#fff2cf"
  error: "#ad2a22"
  error-soft: "#ffebe8"
typography:
  headline:
    fontFamily: "system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif"
    fontSize: "clamp(1.35rem, 1.1rem + 1vw, 2rem)"
    fontWeight: 700
    lineHeight: 1.18
    letterSpacing: "-0.035em"
  title:
    fontFamily: "system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif"
    fontSize: "1rem"
    fontWeight: 700
    lineHeight: 1.18
    letterSpacing: "-0.02em"
  body:
    fontFamily: "system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif"
    fontSize: "1rem"
    fontWeight: 400
    lineHeight: 1.5
  brand:
    fontFamily: "system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif"
    fontSize: "1.08rem"
    fontWeight: 760
    lineHeight: 1.1
  subheading:
    fontFamily: "system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif"
    fontSize: "1.1rem"
    fontWeight: 700
    lineHeight: 1.18
  emphasis:
    fontFamily: "system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif"
    fontSize: "1.05rem"
    fontWeight: 700
    lineHeight: 1.2
  small:
    fontFamily: "system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif"
    fontSize: "0.9rem"
    fontWeight: 400
    lineHeight: 1.5
  label:
    fontFamily: "system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif"
    fontSize: "0.72rem"
    fontWeight: 750
    lineHeight: 1.2
    letterSpacing: "0.055em"
  data:
    fontFamily: "ui-monospace, SFMono-Regular, Consolas, monospace"
    fontSize: "0.8rem"
    fontWeight: 700
  caption:
    fontFamily: "system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif"
    fontSize: "0.78rem"
    fontWeight: 400
    lineHeight: 1.4
  badge:
    fontFamily: "system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif"
    fontSize: "0.68rem"
    fontWeight: 800
    lineHeight: 1
    letterSpacing: "0.055em"
  stage:
    fontFamily: "system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif"
    fontSize: "clamp(0.63rem, 2.4vw, 0.76rem)"
    fontWeight: 700
    lineHeight: 1.2
rounded:
  xs: "3px"
  sm: "4px"
  md: "5px"
  lg: "7px"
  pill: "999px"
  circle: "50%"
spacing:
  xs: "0.35rem"
  sm: "0.6rem"
  md: "1rem"
  lg: "1.5rem"
  xl: "2.25rem"
components:
  button-primary:
    backgroundColor: "{colors.accent}"
    textColor: "{colors.accent-text}"
    rounded: "{rounded.lg}"
    padding: "0.58rem 1rem"
    height: "44px"
  button-primary-hover:
    backgroundColor: "{colors.accent-strong}"
  button-secondary:
    backgroundColor: "{colors.surface-raised}"
    textColor: "{colors.text}"
    rounded: "{rounded.lg}"
    padding: "0.58rem 1rem"
  button-danger:
    backgroundColor: "transparent"
    textColor: "{colors.error}"
    rounded: "{rounded.lg}"
    padding: "0.58rem 1rem"
  badge-ok:
    backgroundColor: "{colors.ok-soft}"
    textColor: "{colors.ok}"
    rounded: "{rounded.xs}"
    padding: "0.2rem 0.58rem"
  badge-error:
    backgroundColor: "{colors.error-soft}"
    textColor: "{colors.error}"
    rounded: "{rounded.xs}"
    padding: "0.2rem 0.58rem"
  badge-busy:
    backgroundColor: "{colors.accent-soft}"
    textColor: "{colors.accent-strong}"
    rounded: "{rounded.xs}"
    padding: "0.2rem 0.58rem"
  instrument-panel:
    backgroundColor: "{colors.surface}"
    rounded: "{rounded.lg}"
    padding: "clamp(1rem, 3vw, 1.65rem)"
  work-order-card:
    backgroundColor: "{colors.surface-raised}"
    rounded: "{rounded.sm}"
    padding: "0"
---

# Design System: Filetwist

## Overview

**Creative North Star: "The Home Media Service Bench"**

Filetwist reads as a service bench, not an upload card: household media arrives as a work order, gets routed through a visible three-stage path (Original → Profile → Output), and leaves with a stamped, validated result. The instrument header up top always frames what the current work order is and what state it's in, before anything else competes for attention. Panels sit on a deeper "bench" background like tools laid out on a counter, each stamped with a thin accent rule across its top edge, the closest thing this system has to a signature mark.

The palette carries the metaphor: graphite-toned bench surfaces hold pale inspection-sheet panels, blue is reserved for controls and the thing currently happening, amber flags a processing note worth a second look, and green is the seal a file only earns once its output validates. Nothing here is decorative; every color, badge, and stage dot reports a real state from the conversion pipeline. Light and dark are both first-class: the bench darkens to true graphite and panels lighten only relative to it, rather than the whole page inverting to a generic dark theme.

**Key Characteristics:**
- Compact instrument header first; work-order state is visible before scrolling.
- A five-stage horizontal path (`.job-path`) shows literal conversion progress, not a spinner.
- Every file becomes a "work order" card with an explicit Original / Profile / Output route.
- Color is a state report (blue = active/control, amber = warning, green = validated, red = failed), never mood decoration.
- Flat panels lifted by a single soft ambient shadow; buttons press down on click rather than glow.

## Colors

The bench is deliberately desaturated blue-grey so that the three semantic accents (blue, amber, green) read as signals, not as one voice among many.

### Primary
- **Control Blue** (`--accent` `#1769e0` light / `#6daaff` dark): the only color assigned to interactive controls, links, the current-stage marker, and focus treatment. Never used for status text.
- **Control Blue, Pressed** (`--accent-strong` `#0e4fae` light / `#9bc6ff` dark): hover/active button state and stronger link text.
- **Control Blue, Wash** (`--accent-soft` `#e4efff` light / `#173457` dark): backgrounds for the current stage's glow ring, the download-actions panel, and busy badges.

### Neutral
- **Bench** (`--bench` `#dce3e8` light / `#141a21` dark): the page's outermost surface — the counter the panels sit on.
- **Bench, Deep** (`--bench-deep` `#cbd4db` light / `#0f141a` dark): the header/nav band, one step darker than the bench itself.
- **Inspection Sheet** (`--surface` `#f8f9f7` light / `#1c242d` dark): the pale panel background — instrument panels and cards.
- **Inspection Sheet, Raised** (`--surface-raised` `#ffffff` light / `#222c36` dark): work-order cards, selects, secondary buttons — one layer up from the panel they sit inside.
- **Inspection Sheet, Alt** (`--surface-alt` `#e9eef1` light / `#27323d` dark): file-header bands, drop-zone rest state, hover rows.
- **Ink** (`--text` `#111820` light / `#f1f4f6` dark): primary text.
- **Ink, Muted** (`--muted` `#52616f` light / `#b5c0ca` dark): captions, hints, secondary metadata.
- **Border** (`--border` `#788592` light / `#71808f` dark) and **Border, Soft** (`--border-soft` `#c8d0d6` light / `#3d4a56` dark): structural rules between panel sections, table cells, and route columns.

### Named Rules
**The One Signal Rule.** Amber (`--warn`) means "read this before you trust the output"; it never appears on an error. Red (`--error`) means the operation failed; it never appears on a warning. Green (`--ok`) is earned only by a passed validation check, a completed stage, or the retention indicator — never a generic "success" toast.

## Typography

**Body/UI Font:** `system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif` (single stack for everything; no display or mono webfont is loaded).
**Data/Mono Font:** `ui-monospace, SFMono-Regular, Consolas, monospace` — job codes and technical readouts only.

**Character:** A single, neutral system stack kept honest by weight and tracking rather than by font choice — this is instrument-panel typography, not editorial typography.

### Hierarchy
- **Headline** (700, `clamp(1.35rem, 1.1rem + 1vw, 2rem)`, 1.18 line-height, -0.035em tracking): panel titles ("Media intake", "Recent work orders", a job's heading).
- **Title** (700 via bold markup, 1rem, -0.02em tracking): work-order file names (`.file-header h3`), diagnostic block headings.
- **Body** (400, 1rem/1.5): running copy, lede paragraphs (max-width `72ch` via `--measure`).
- **Small body** (400, 0.9rem/1.5): privacy notes, selected-file status, profile descriptions, and dense diagnostics.
- **Brand / emphasis / subheading** (1.08rem / 1.05rem / 1.1rem): the wordmark, drop-zone action, and help-section headings.
- **Label** (750, 0.72rem, 0.055em tracking, uppercase): field captions (`job-facts dt`, `route-label`, `technical-readout dt`) and the brand tagline.
- **Data / caption / badge** (0.8rem / 0.78rem / 0.68rem): technical readouts, recommendations, and compact state stamps.
- **Stage** (`clamp(0.63rem, 2.4vw, 0.76rem)`): the fixed five-step progress path, allowed to scale only to keep the complete sequence visible.

### Named Rules
**The Tabular-Nums Rule.** Any number that represents a measured fact (byte size, file count, queue depth) uses `font-variant-numeric: tabular-nums` so a work order's figures don't visually jitter as they update over HTMX polling.

## Layout

The page is a single centered column: `main` caps at `76rem`, the header/nav band at `72rem`, both `margin-inline: auto` with `clamp(1rem, 3vw, 2.25rem)` outer padding. There is no sidebar or persistent nav beyond the header; every page is one scroll of stacked instrument panels.

Two-column and multi-column layouts only appear where the content has a real relational structure to show:
- `.job-facts` (files / intake size / retention): 2 columns by default, 3 columns at `34rem`.
- `.file-route` (Original / Profile / Output): stacked at 1 column below `46rem`, a `0.85fr / 1.45fr / 1fr` 3-column grid above it — the conversion route only becomes a literal left-to-right path once there's room to read it that way.
- `.job-path` (5-stage tracker): always a fixed 5-column grid, sized down via `clamp()` font rather than reflowing, because the stage sequence must stay legible as a single strip at any width.
- `.help-layout` and `.diagnostic-grid`: single column until `62rem`, where a sticky 14rem nav rail or a 2-column diagnostic grid appears.

Breakpoints in use: `34rem` (~544px, facts/actions go inline), `46rem` (~736px, route becomes 3 columns, tagline/description reappear), `62rem` (~992px, help/diagnostics gain a second column). `prefers-reduced-motion` collapses all transitions to 0.01ms; `prefers-contrast: more` doubles border widths and pins `--border` to `currentColor`.

## Elevation & Depth

Flat by default, lifted only where something is a discrete object on the bench. Instrument panels get one soft ambient shadow (`0 9px 24px var(--shadow)`) to read as objects sitting on the bench surface, while work orders stay flush inside them as ledger entries. Buttons and the brand/drop-zone marks get a tighter, more directional shadow so they feel like small physical controls rather than flat labels.

### Shadow Vocabulary
- **Panel lift** (`box-shadow: 0 9px 24px var(--shadow)`): `.instrument-panel`, `.card`. The bench-to-panel separation.
- **Control lift** (`box-shadow: 0 3px 8px var(--shadow)`, hover `0 5px 13px`, active `0 2px 5px` + `translateY`): buttons, brand mark, drop-zone mark. Presses down on click; lifts on hover.
- **State glow** (`box-shadow: 0 0 0 4px var(--accent-soft|--error-soft)`): the current or errored stage dot in `.job-path`. A halo, not a drop shadow — it reads as "this one is active," not "this one is elevated."

### Named Rules
**The Press-Down Rule.** Every clickable button lifts 1px on hover and drops 1px on `:active`. A control that doesn't physically respond to a click doesn't belong in this system.

## Shapes

Two radius tiers carry the whole system: `7px` (`--radius`) for anything that behaves like a panel or a primary control (instrument panels, buttons, the drop-zone, the skip link, selects), and `3–5px` for smaller, denser objects nested inside a panel (badges at 3px, work-order cards and help-nav links at 4px, the brand mark at 5px). Fully round shapes (`50%`, `999px`) are reserved for indicators, never for content containers: the stage-mark dot, the retention light, and the progress bar track.

## Components

### Buttons
- **Shape:** `7px` radius (`--radius`), `44px` minimum height/width (`--control-size`) as the touch-target floor.
- **Primary:** solid Control Blue (`--accent`) fill, white text, `0.58rem 1rem` padding.
- **Secondary:** `--surface-raised` fill with a `--border` outline; used for "Cancel," "Download all files," secondary navigation.
- **Danger:** transparent fill with a red outline and red text at rest, filling to `--error-soft` on hover — reserved for destructive actions ("Delete job").
- **Text button:** transparent, underlined, Control-Blue-Strong text — used for inline actions like "Remove file."
- **Hover/Focus:** hover lifts 1px and deepens the shadow; `:focus-visible` gets a `3px` solid accent outline with `3px` offset on every interactive element, not just buttons.
- **Disabled:** `opacity: 0.52`, no transform, `cursor: not-allowed`.

### Badges
- **Style:** small pill-cornered (`3px` radius) uppercase label, `0.68rem`, weight 800, `0.055em` tracking.
- **State:** `badge--ok` (green), `badge--error` (red), `badge--busy` (blue) — the only three states a work order or file can carry.

### Panels / Cards
- **Corner Style:** `7px` radius for `.instrument-panel`/`.card`; `4px` for the nested `.work-order` card.
- **Signature mark:** a `3px` accent-colored rule inset `1.25rem` from each panel edge, sitting just above the panel's top border (`::before` on `.instrument-panel`/`.card`). This is the recurring signature element of the whole system — every panel gets one, always static, never a hover reveal.
- **Shadow Strategy:** see Elevation & Depth → Panel lift.
- **Internal Padding:** `clamp(1rem, 3vw, 1.65rem)`.

### Job Path (signature component)
The `.job-path` ordered list is the system's defining custom component: a fixed 5-stage horizontal tracker connected by a `--border-soft` line. Completed stages turn the connecting line and dot green (`--ok`); the current stage gets a blue dot with a soft halo; a failed stage gets a red dot with a red halo. This is the only place in the system where color communicates progress through time rather than a static state.

### Work Order / File Route
Each uploaded file renders as a `.work-order` card: a header (filename + state badge) over a `.file-route` grid with three stages — **Original** (size, detected media), **Profile** (the operation select or its locked label, plus a recommendation), **Output** (result name, size, download). Below it, warnings (`.message.warning`), a validation seal (`.validation-result`, dot + message), and a collapsible `.conversion-details` monospace readout (detected format, validation, execution, output) sit in a fixed order — the route always resolves top-to-bottom into a verdict.

### Inputs / Selects
- **Style:** `--surface-raised` background, `1px` `--border` outline, `7px` radius, `44px` minimum height.
- **Focus:** `3px` solid accent outline, `3px` offset — identical treatment to buttons and links, one focus language for the whole page.

### Navigation
- The header is a single row: brand mark + wordmark on the left, one utility link ("Help & file handling") on the right, both meeting the `44px` control-size floor. The help page's side nav (`.help-nav`) becomes sticky only at `62rem`; below that it's an inline list at the top of the page.

## Do's and Don'ts

### Do:
- **Do** treat blue as the only accent for controls, links, and "this is active right now" states (The One Signal Rule).
- **Do** put a `3px` accent rule across the top of every instrument panel; it is the system's one recurring signature mark, and it stays static.
- **Do** use monospace tabular-nums for job codes and technical readouts (The Tabular-Nums Rule); use system-font tabular-nums for plain numeric facts like file counts.
- **Do** keep every interactive control at a `44px` minimum touch target (`--control-size`).
- **Do** show conversion progress with the literal 5-stage `.job-path` tracker rather than an indeterminate spinner whenever a file's route can be described in stages.

### Don't:
- **Don't** use amber (`--warn`) for a failed operation, or red (`--error`) for a non-fatal note — the two vocabularies never swap roles.
- **Don't** award a green validation seal for anything short of a passed output check; it is not a generic "done" color.
- **Don't** collapse the three-column Original/Profile/Output route into an unstructured list on desktop; the columns are the story ("bring media in, choose what changes, take the result home") made visible, not a layout convenience.
- **Don't** add a second, heavier elevation style (large drop shadows, glassmorphism, gradients); every surface uses the same flat-with-one-ambient-shadow model (The Press-Down Rule for controls, Panel lift for containers).
- **Don't** open the first viewport with hero imagery, marketing copy, or a generic upload card; the instrument header and current work order state come first, because this is a service bench, not a landing page.

---
target: Filetwist web conversion flow
total_score: 22
max_score: 40
na_heuristics:
p0_count: 0
p1_count: 3
timestamp: 2026-09-06T21-02-48Z
slug: internal-web-templates-index-html
---
Method: DEGRADED, single-context (sequential assessments of the small web interface).

# Filetwist conversion-flow critique

Target: `internal/web/templates/index.html`, including its shared job workflow.
Design review was recorded before the deterministic scan. Browser evidence
covers 1280px desktop and 390px mobile, light/dark appearances, and actual
conversions of synthetic PNG and silent MP4 files. This is a heuristic review,
not a usability study or accessibility certification.

## Design specificity and overall impression

**22/40: Acceptable. The interface works, but still speaks more like a converter
console than a household tool.** Its restrained styling is coherent, yet the
generic upload box, nested cards, and status badges could belong to almost any
file utility. Product character should come from understandable conversion
choices and trustworthy handling of files, not additional decoration.

The biggest opportunity is to make each stage answer the user's immediate
question: what will change, what is happening, and what should I download?

## Design health

Scores are positive, from 0 (absent) to 4 (excellent). All ten heuristics apply.

| # | Heuristic | Score | Key issue |
|---|-----------|-------|-----------|
| 1 | Visibility of system status | 2/4 | Status exists, but the empty uploader stays prominent after success. |
| 2 | Match with the real world | 2/4 | Presets lack outcome explanations; raw format identifiers leak through. |
| 3 | User control and freedom | 3/4 | Cancel and confirmed deletion exist; no individual file removal before conversion. |
| 4 | Consistency and standards | 3/4 | Consistent controls and states; success lacks a primary download action. |
| 5 | Error prevention | 2/4 | Eligible defaults help, but shared access and profile consequences are not explained in context. |
| 6 | Recognition rather than recall | 2/4 | Recent jobs are identified by random short IDs, not recognizable contents. |
| 7 | Flexibility and efficiency | 2/4 | Batch upload and ZIP download exist; changing a batch's preset remains per-file. |
| 8 | Aesthetic and minimalist design | 2/4 | The uploader and technical metadata compete with the current task. |
| 9 | Error recovery | 3/4 | Failed requests preserve selections, but errors appear in a distant global notice. |
| 10 | Help and documentation | 1/4 | Useful repository documentation has no entry point in the interface. |
| **Total** | | **22/40** | **Acceptable** |

## What's working

- Inspection before conversion and eligible recommendations reduce invalid
  choices without exposing all eight operations at once.
- Failed-action handling preserves selections and download controls; deletion
  explicitly confirms that uploaded and converted files will be removed.
- The layout wraps at 390px without horizontal overflow in the inspected states.
  Light/dark styles, keyboard upload activation, labels, and status announcements
  are already present.

Sources: [job controls](https://github.com/guigui42/filetwist/blob/main/internal/web/templates/job.html#L15-L60),
[error handling](https://github.com/guigui42/filetwist/blob/main/internal/web/static/app.js#L10-L38),
[upload controls](https://github.com/guigui42/filetwist/blob/main/internal/web/templates/index.html#L4-L26).

## Priority issues

1. **P1: Shared access is invisible before upload.** The page explains expiry,
   but not that everyone with service access can download and delete jobs.
   That is a consequential omission for household media. Put a short shared-
   workspace notice beside upload, with a local explanation of retention and
   deletion. Do not imply individual accounts or anonymization.
   Command: `/impeccable clarify`.
   Sources: [upload page](https://github.com/guigui42/filetwist/blob/main/internal/web/templates/index.html#L1-L28),
   [documented sharing model](https://github.com/guigui42/filetwist/blob/main/docs/web.md#shared-jobs).

2. **P1: Presets name intentions, not consequences.** "Compatible photo",
   "Smaller photo", and "Lossless image" do not reveal JPEG/WebP/PNG or the
   relevant tradeoffs. Add concise selected-preset help before Convert:
   output format, important changes, and the limitation that matters for this
   input. Keep codec detail available separately for enthusiasts.
   Command: `/impeccable clarify`.
   Sources: [selector](https://github.com/guigui42/filetwist/blob/main/internal/web/templates/job.html#L24-L34),
   [actual guarantees](https://github.com/guigui42/filetwist/blob/main/docs/web.md#conversion-profiles).

3. **P1: The page does not move with the task.** After upload, the first large
   section resets to "No files selected" while review appears below. After
   conversion, every download remains secondary, and mobile users still scroll
   past that empty uploader. Make the active stage prominent, move focus
   deliberately after transitions, keep "Upload more" available compactly, and
   make the completed state's main action a download. Keep technical execution
   details available behind an explicit disclosure.
   Command: `/impeccable layout`.
   Sources: [upload reset](https://github.com/guigui42/filetwist/blob/main/internal/web/static/app.js#L151-L162),
   [result hierarchy](https://github.com/guigui42/filetwist/blob/main/internal/web/templates/job.html#L35-L60).

4. **P2: Batch work creates avoidable repetition and recall.** A non-default
   preset requires one selection per eligible file. Recent jobs expose short
   IDs, counts, and dates, but no filename preview. Add an explicit apply-to-
   compatible-files action that preserves per-file overrides; identify jobs by
   recognizable contents as well as their IDs.
   Command: `/impeccable shape`.
   Sources: [per-file choices](https://github.com/guigui42/filetwist/blob/main/internal/web/templates/job.html#L17-L33),
   [recent jobs](https://github.com/guigui42/filetwist/blob/main/internal/web/templates/index.html#L32-L47).

5. **P2: Important controls are cramped for touch.** Rendered operation selects
   measured about 33px high, standard action buttons 42px, and single-line
   individual download links 38px. Give these controls at least a 44px touch
   area without inflating all metadata. This is a touch-comfort recommendation,
   not a claim that each control violates WCAG.
   Command: `/impeccable adapt`.
   Source: [control styling](https://github.com/guigui42/filetwist/blob/main/internal/web/static/app.css#L158-L179)
   and [select styling](https://github.com/guigui42/filetwist/blob/main/internal/web/static/app.css#L259-L267).

## Cognitive load and emotional journey

**Moderate cognitive load: three checklist weaknesses.** Visual hierarchy,
working-memory support, and progressive disclosure need work. Native selectors
exposed only two or three eligible choices in the inspected cases, so this is
not a wall-of-options problem.

The emotional valley comes immediately after upload: an empty-looking form
dominates while the next step sits below it. The ending emphasizes job status
and execution metadata rather than a clearly prioritized result download.

## Persona red flags

- **Jordan, household first-timer:** can find upload, but cannot tell what
  "lossless" preserves or who else can access the files.
- **Alex, self-hosting power user:** can batch upload and download, but must
  repeatedly change the same preset and recognize jobs from opaque IDs.
- **Casey, mobile user:** must scroll past the reset uploader to continue;
  small selects and verbose result rows add avoidable effort.

## Minor observations

The observed video summary exposes `mov,mp4,m4a,3gp,3g2,mj2`; use a readable
format label and retain probe detail in a disclosure. Replace `file(s)` with
correct pluralization. Long filenames wrap without overflow, but `break-all`
splits words and makes the same filename occupy several lines twice.

## Detector evidence and limitations

The CLI detector reported **0 findings** for `internal/web/templates`. This
does not establish that rendered CSS or workflow design is correct.

Browser mutation preflight succeeded, but loading the detector overlay was
blocked by the application's `script-src 'self'` Content Security Policy.
No reliable user-visible overlay is available. The policy was not weakened.
Manual browser screenshots and DOM measurements supplied the visual evidence;
there are no browser detector findings or false positives to reconcile.

The browser canvas opened, but did not return the page identifier required by
its inspection actions, so browser inspection used headless Playwright in fresh
pages. No Computer Use automation was used. Both temporary servers were stopped.

## Design decision to resolve

Prioritize informed conversion choices and stage progression over a cosmetic
redesign. Preserve technical detail for enthusiasts without making it the
default reading path for household members.

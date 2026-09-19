# Design system — operator consoles

This is the design record for the look and feel of
[the console](console.md), written so a *different* app in this product
family comes out looking and behaving like it without anyone having to
reverse-engineer `cmd/console/web/app.css`.

**This document is the source of truth.** `cmd/console/web/app.css` is one
instance of it, not the definition. If the two disagree, the document wins
and the stylesheet is the thing that is wrong.

## How to use it

Three ways, all supported on purpose:

1. **Implicitly.** The repo's `CLAUDE.md` points an agent here whenever it
   touches a UI. Nothing to say in the prompt.
2. **Explicitly.** "Build X, follow `docs/design-system.md`." Use this for
   an app in another repo — copy this file across with it.
3. **By editing.** Change a token, a rule or a "don't" below, and the next
   thing built picks the change up. That is the intended way to evolve the
   system: edit the document, then bring the implementations to it.

If you only read one section, read [Colour law](#colour-law) and
[Behaviour contracts](#behaviour-contracts) — those two are what make an
app *feel* like this one. The tokens only make it *look* like it.

---

## The brief in one paragraph

An **operator console for a system that handles money**: dense, dark by
default, monospace wherever a value is something you might paste into a
terminal, and legible at a glance from two metres away during a demo. It
should read as a control panel someone technical built for themselves —
not as a marketing site, not as an enterprise dashboard product. Every
surface tells the truth about what it knows, including when it knows
nothing. Purple is the identity; green and red are *data*, never
decoration.

## Non-negotiables

These constraints produced the look. Drop one and the result stops
belonging to this family.

| Constraint | Why |
| --- | --- |
| **No build step, no bundler, no framework** | One HTML, one CSS, one JS file, served as written. The app must survive `git clone` and nothing else. |
| **No CDN, no external host, no web font** | These apps run on a laptop with no network. A `fonts.googleapis.com` link is the first thing to break, and it breaks silently — a page that renders in a fallback font on the day of the demo. Assert it in a test (see below). |
| **Ships inside the binary** | `//go:embed web`. One artefact, no static-file deployment step. |
| **Dark first, light behind a toggle** | Both are first-class; neither is a filter over the other. |
| **Status colour is reserved for status** | Nothing decorative is ever green or red. |
| **The UI is a client, never a back door** | Every button is exactly one call to a public API a human could make with `curl`. No state exists that is only reachable by clicking. |

The no-external-host rule is enforceable, and should be enforced. The
console's test:

```go
external := regexp.MustCompile(`(?i)(https?:)?//(?:cdn|unpkg|fonts\.|ajax\.|code\.jquery)`)
```

served against every embedded asset. Copy it into any new app.

---

## Tokens

Canonical. Copy verbatim; do not re-pick these by eye.

```css
:root {
  /* surfaces, darkest to lightest — four steps, no more */
  --bg: #0e0c14;          /* the page */
  --panel: #16131f;       /* cards, sidebar, modal */
  --panel-2: #1c1828;     /* inputs, ghost buttons, toasts */
  --panel-3: #221d30;     /* badges, hover, scrollbar thumb */
  --border: #2c2540;      /* card and control edges */
  --border-soft: #241f33; /* row separators, internal rules */

  /* text, three weights of presence */
  --text: #ece9f5;        /* anything you must read */
  --text-dim: #a49bba;    /* supporting prose, secondary values */
  --text-faint: #837a99;  /* labels, units, metadata */

  /* identity */
  --accent: #a855f7;
  --accent-hi: #c084fc;
  --accent-lo: #7c3aed;
  --accent-ghost: rgba(168, 85, 247, 0.14);   /* tinted fills */
  --accent-line: rgba(168, 85, 247, 0.35);    /* hover/active edges */
  --accent-btn-hi: #9333ea;  /* primary button gradient, light stop */
  --accent-btn-lo: #7c3aed;  /* primary button gradient, dark stop */

  /* status — data only */
  --up: #4ade80;
  --down: #fb7185;
  --warn: #fbbf24;
  --unknown: #8b8299;

  --radius: 12px;
  --radius-sm: 8px;
  --mono: ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, monospace;
  --sans: ui-sans-serif, system-ui, -apple-system, "Segoe UI", Roboto, sans-serif;
}

:root[data-theme="light"] {
  --bg: #f7f5fb;
  --panel: #ffffff;
  --panel-2: #f4f1fa;
  --panel-3: #ece7f7;
  --border: #ded6ee;
  --border-soft: #e9e3f5;
  --text: #1d1730;
  --text-dim: #5d5478;
  --text-faint: #767089;
  --accent: #7c3aed;
  --accent-hi: #9333ea;
  --accent-lo: #6d28d9;
  --accent-ghost: rgba(124, 58, 237, 0.10);
  --accent-line: rgba(124, 58, 237, 0.28);
  --accent-btn-hi: #9333ea;
  --accent-btn-lo: #6d28d9;
  --up: #15803d;
  --down: #c81e1e;
  --warn: #b45309;
  --unknown: #6f6885;
}
```

Notes on the values, because they are not arbitrary:

- **Four surface steps, and they are close together.** `--bg` → `--panel`
  is an 8-point lift, not a 30-point one. Density comes from many quiet
  edges, not from high-contrast slabs.
- **Purple is warm-shifted into the greys.** `--bg` is `#0e0c14`, not
  `#0e0e0e`. Every neutral in the dark palette carries a little violet, and
  that is what stops the accent looking stuck on.
- **`--accent` is the identity, `--accent-hi`/`--accent-lo` are the
  gradient.** Gradients run `140deg`, high → low. Never the reverse.
- **`--accent-btn-*` exists separately from `--accent-hi`/`--accent-lo`**
  because white text on `#c084fc` is 2.6:1 — a filled button using the raw
  identity gradient is unreadable at its light end. See
  [Contrast](#contrast-the-measured-floor).
- **`--unknown` is a grey, deliberately.** "I have not checked" must not
  look like "it is fine" or "it is broken".

### Theme

Dark is the default; light is a real theme, not a filter. Respect the OS
on first paint and let an explicit choice win afterwards:

```html
<html lang="en" data-theme="dark">
```

```js
try {
  const saved = localStorage.getItem('<app>-theme');
  if (saved) document.documentElement.dataset.theme = saved;
  else if (matchMedia('(prefers-color-scheme: light)').matches)
    document.documentElement.dataset.theme = 'light';
} catch (_) { /* private window: the default stands */ }
```

Every `localStorage` read and write is wrapped — a private window throws
on access, and a UI that white-screens because it could not remember a
theme preference is a UI nobody trusts with a payout button.

### Type

One sans stack for prose, one mono stack for values. No third family, no
web font.

| Use | Size | Notes |
| --- | --- | --- |
| Body | 14px / 1.5 | `html, body` |
| View title (`h1`) | 19px, 600, `-0.2px` | one per screen |
| Card title | 13.5px, 600 | |
| Body copy in cards, tables | 12.5px | |
| Section heading | 12px, 600, uppercase, `.08em` | always `--text-faint` |
| Field label | 10.5px, uppercase, `.06em` | always `--text-faint` |
| Pill | 10.5px mono, uppercase, `.06em` | |
| Mono value | 11.5px | one notch smaller than the prose beside it |
| Stat number | 21px, 600, `tabular-nums` | |

**Mono is not styling, it is a claim.** A value is monospace if and only if
it is a machine identifier a human might copy: an id, a MID, an IBAN, a
URL, a path, an amount, a duration, an env var name. A *legal name* is
sans. A *description* is sans. This is why the console overrides
`style="font-family:var(--sans)"` on some `<dd>` elements inside a `.kv`
whose default is mono — the exception proves the rule is being applied.

Everything numeric that sits in a column gets
`font-variant-numeric: tabular-nums`.

### Space, radius, motion

- Rhythm is **10 / 12 / 16 / 20 / 26px**. Content padding is `20px 26px`,
  card padding `13px 15px`, gaps `8–12px`.
- Radius: `12px` for cards and stats, `8px` for controls and inputs,
  `14px` for modals, `5px` for pills, `20px` for badges.
- Motion is **120ms** for state (`background`, `color`, `border-color`),
  **150ms** for the disclosure chevron, **180ms** for a toast entering.
  Nothing animates longer than 180ms; nothing eases in on load. Respect
  `prefers-reduced-motion`:

```css
@media (prefers-reduced-motion: reduce) {
  * { animation-duration: .01ms !important; transition-duration: .01ms !important; }
}
```

---

## Colour law

The rule that carries most of the identity:

> **Purple is decoration. Green, red and amber are data. A colour never
> crosses that line.**

Concretely:

- A primary button is purple. A "success" button is *still purple* — the
  success is reported afterwards, by a toast whose left border is green.
- A selected nav item is purple. An active tab is purple.
- Green appears only as: a health dot, an `ok` pill, a `.stat.up` number, a
  sparkline bar, a good toast's border. Same for red, inverted.
- Amber (`--warn`) means *degraded or caveated*, not *severe*. A
  `.note.warn` says "this vendor has no such endpoint" — it is not an
  error, and must not be red.
- Grey (`--unknown`) means *not yet known*. Reach for it more often than
  feels natural. **Never report unknown as bad.** A status page that cries
  wolf on start-up is a status page people learn to ignore.

Status tints are always derived, never hand-picked:

```css
.pill.ok   { color: var(--up);   border-color: color-mix(in srgb, var(--up)   35%, transparent); }
.note.bad  { border-left-color: var(--down); background: color-mix(in srgb, var(--down) 9%, transparent); }
.dot.up    { background: var(--up); box-shadow: 0 0 0 3px color-mix(in srgb, var(--up) 18%, transparent); }
```

Three derivation strengths, and only three: **9%** for a note's wash, **18%**
for a dot's halo, **35%** for a border.

### Contrast: the measured floor

Measured against `--panel`, WCAG 2.1:

| Pair | Dark | Light |
| --- | --- | --- |
| `--text` | 15.3 | 17.3 |
| `--text-dim` | 7.0 | 7.0 |
| `--text-faint` | **4.5** | **4.7** |
| `--accent` | 4.6 | 5.7 |
| `--up` | 10.5 | 5.0 |
| `--down` | 6.8 | 5.7 |
| `--warn` | 11.0 | 5.0 |
| white on primary button | 5.4–5.7 | 5.4–7.1 |

Floor: **4.5:1 for anything a user has to read**, including 10.5px
uppercase labels — small caps are the *hardest* text on the page, so they
do not get the large-text exemption. Three of the values in the token block
above exist because the console's originals missed that floor:
`--text-faint` (was `#6f6685`, 3.4:1), light `--up` (was `#16a34a`, 3.3:1
on white, and `.pill.ok` renders it at 10.5px), and the primary-button
gradient. Do not revert them.

Colour is never the only channel. A health dot carries a shape difference
too (`not-probed` is a dashed ring, not a filled circle); a state is always
spelled out in words somewhere in the expanded view.

---

## Layout

```
┌────────────┬──────────────────────────────────────────┐
│ brand      │ topbar: h1 + one-line sub | actions      │
│ nav        ├──────────────────────────────────────────┤
│  ▸ item    │ content — the only scrolling region      │
│  ▸ item    │   section title                          │
│            │   ┌ card ─────────────────────────────┐  │
│            │   └───────────────────────────────────┘  │
│ legend     │                                          │
│ theme      │                                          │
└────────────┴──────────────────────────────────────────┘
```

- `.app { display: flex; height: 100vh; overflow: hidden; }` — the shell
  never scrolls; `.content` does. The topbar and nav stay put while you
  read a long table.
- Sidebar is **232px**, fixed, `--panel`, with a right border *and* a
  vertical accent gradient bleeding through it:

```css
.sidebar::after {
  content: ""; position: absolute; inset: 0 -1px 0 auto; width: 1px;
  background: linear-gradient(180deg, transparent, var(--accent-line) 25%,
                                      var(--accent-line) 60%, transparent);
}
```

  That one line does more for the identity than anything else in the
  stylesheet. Keep it.

- Topbar: `align-items: flex-end`, `linear-gradient(180deg, var(--panel), var(--bg))`,
  bottom border. Title plus **one sentence** of sub-copy, `max-width: 78ch`.
  Actions right, `flex: none`.
- Content: `padding: 20px 26px 60px`. The 60px bottom is deliberate — the
  last card must not sit against the viewport edge.
- Below **760px** the sidebar collapses to a 62px icon rail
  (`.brand-text, .nav-label, .nav-badge, .legend { display: none }`) and
  horizontal padding drops to 14px. That is the only breakpoint. These are
  desktop tools; a phone layout would be a different product.

Every wide thing (tables, code, diagrams) scrolls inside its own
`.table-scroll { overflow-x: auto }`. The page body never scrolls sideways.

---

## Components

### Sidebar nav item

A `<button>`, never an `<a>` — these are view switches, not navigations.
Active state is a ghost-purple fill plus a 3px gradient rail bleeding off
the left edge (`::before` at `left: -10px`). Icon is a **Unicode glyph**
(`▤ ▦ ◍ ⚙ ◈ ▸ ◐`), 16px wide, centred, `--accent`. No icon font, no SVG
sprite — a glyph is one character and cannot fail to load.

Trailing `.nav-badge` carries a live count (`3/7`, `12`). It is
`:empty { display: none }`, so it simply is not there before the first poll
returns rather than flashing a zero.

### Card — the workhorse

Everything that is a *thing* is a card: a service, a bank, a merchant.
Collapsed it is one row; expanded it is the full record.

```html
<div class="card is-open">
  <div class="card-head" data-card="{id}">
    <span class="chev">▸</span>
    <i class="dot up"></i>
    <div class="card-title">
      <span class="name">Worldline <span class="pill vendor">vendor</span></span>
      <span class="desc">One-line summary, ellipsised when collapsed.</span>
    </div>
    <div class="card-meta"><span class="spark">…</span><span>34ms</span></div>
  </div>
  <div class="card-body">…</div>
</div>
```

Rules:

- The **head is the whole hit target**, but clicks on
  `button, a, input, select, textarea, label, .form` are excluded — a form
  laid out inside a card must not collapse it when the operator clicks the
  gap between two fields.
- `.desc` is `white-space: nowrap` + ellipsis when collapsed, `normal` when
  open. One line at rest, full text when you have asked for it.
- Open state is **held in app state keyed `view:id`**, not in the DOM, so a
  background poll re-render does not collapse what you are reading.
- Border goes `--accent-line` on both `:hover` and `.is-open`. Hover and
  open are the same visual affordance.
- The chevron rotates 90°, 150ms.

**Accessibility:** the head must be reachable and operable from the
keyboard — `tabindex="0"`, `role="button"`, `aria-expanded`, and Enter/Space
handled alongside the click. The console does not do this yet; new apps
should.

### Sequence diagram

The home view of an app that drives a system: participants as boxes across
the top, lifelines down, one arrow per hop, lighting as its own traffic
arrives.

**Hand-drawn SVG, not a diagram library.** Two reasons, and the second is
the deciding one. A CDN breaks the first non-negotiable in this document,
and a vendored bundle is a megabyte for one picture. More importantly, a
library that renders from a text description rebuilds its DOM on every
change — and the whole value here is that *one* arrow animates while the
rest hold still. You cannot animate an element you replace every second.

**Six states, and they are the vocabulary:**

| State | Means | Colour |
| --- | --- | --- |
| idle | nothing has come this way | `--flow-idle`, a dark grey |
| active | something arrived in the last second | `--up` |
| busy | a batch is in flight, verdict unknown | `--flow-busy`, the brightest neutral |
| done | finished, and it was fine | `--flow-done-ok`, a dark green |
| partial | some of a batch was refused | `--warn` |
| failed | it broke | `--down` |

Everything at rest is grey **on purpose**: if the resting state carried any
colour, the one arrow that just moved would compete with eleven that did
not. Participants get their own, quieter scale — active while their traffic
moves, `--flow-done` light grey once it has been through.

**A box reports on the participant, an arrow on the hop.** One report stage
failing on a mail hop does not make the bank unwell. Red on a box means the
service is unreachable; red on an arrow means that traffic failed. A box
that means "something that touched this went wrong" is a box people stop
believing.

**Hold every state for at least a second.** A hop can take three
milliseconds; without a hold it repaints twice between two frames and is
never seen. The glow ramps up and back down across the same 1.2s
(`@keyframes flow-pulse`), so the eye is drawn and then released. Under
`prefers-reduced-motion` the colour still changes and the hold still
applies — only the ramp goes.

**The server counts, the browser notices.** The endpoint reports how many
messages each hop has carried; the view remembers what that number was a
poll ago. "Something just happened" is a difference between two
observations, and only the client is in a position to see it. It also means
the server stays a pure function of what the services say.

**Poll faster, but only while it matters.** The base rate stays the app's
usual 5s; the diagram runs at 1s *while a run is in flight*. A run takes
forty seconds, and an arrow lighting four seconds late is not a sequence,
it is a summary.

**Say what a hop is for.** Each arrow carries a `<title>`: why it exists,
and the peer's own words for the last thing through it. An arrow that lights
from background polling rather than from the run says so there, rather than
implying a story that did not happen.

### Activity panel

A vendor that is being driven by another system needs somewhere to say what
that system just did to it. The panel is a **card nested inside a card**:
same chevron, same open affordance, same `view:id` state key — the nested
one keys on `"<service>:<log>"`, which is unique without inventing a second
mechanism.

```html
<div class="card log is-open">
  <div class="card-head" data-card="b4b:payments" role="button" tabindex="0" aria-expanded="true">
    <span class="chev">▸</span>
    <div class="card-title">
      <span class="name">Payouts received <span class="pill">6 total</span> <span class="pill bad">2 failed</span></span>
      <span class="desc">payout 733.34 EUR to ben_… — 12s ago</span>
    </div>
  </div>
  <div class="card-body">
    <div class="note">What this log is for.</div>
    <div class="log-rows">
      <div class="log-row warn">
        <span class="log-time">22:19:03</span>
        <span class="log-op">payment.create</span>
        <span class="log-main">
          <span class="log-summary">payout 951.98 EUR to ben_7 — beneficiary is sanctions-blocked</span>
          <span class="log-detail">amount=951.98 EUR  external_ref=sttl_v1:…</span>
        </span>
        <span class="log-peer">10.89.0.1</span>
      </div>
    </div>
  </div>
</div>
```

Rules, and they are the whole component:

- **Collapsed, the header is the headline.** Counts as pills — total is
  neutral, refusals amber, failures red — and the most recent event's own
  summary as the `.desc`. An operator should be able to close every panel
  and still see, from four collapsed headers, which hop is broken.
- **A good row is not coloured.** Only `.warn` and `.bad` take a left
  border and a wash (6% / 9%). Painting every accepted call green makes the
  two that are not green *harder* to find. This is the same reason
  `--unknown` exists.
- **The row carries the peer's own words.** A refusal's summary ends with
  the vendor's message verbatim, exactly as a toast does. "Payout rejected"
  is not a log line.
- **The body scrolls, the card does not grow.** `max-height: 340px` with
  its own `overflow-y`, so a hundred rows never push the rest of the card
  off the screen.
- **Say when the list is a window.** "Showing the last 100 of 4000" under
  the rows; a ring buffer that silently drops history is a ring buffer
  people misread.
- **The empty state names what would fill it** — "Nothing yet. A payout
  from the platform lands here the moment it is asked for" — never "no
  events".
- **Fetch only while it is being read.** The parent card being open is the
  signal; polling every vendor's log for nobody is traffic with no reader.
- **An unreachable service is not an empty list.** The panel renders
  `.note.bad` with the error. Those two states look identical once the
  failure is swallowed, and they send you to opposite ends of the stack.
- A service that keeps no log renders **no panel at all**, and its API
  answers `501` — the same rule as everything else here: never offer what
  the backend cannot do.

### Pill

`10.5px` mono, uppercase, one-word. Two families that must not be confused:

- **Classification** — `vendor` (accent), `platform` (dim), `supporting`
  (faint). Neutral. Says what kind of thing this is.
- **Status** — `ok`, `bad`, `warn`. Coloured, derived at 35%.

Never invent a third colour. If a new classification is needed it is a
grey.

### Health dot

8px circle, four states, and the fourth is the point:

| Class | Meaning | Look |
| --- | --- | --- |
| `.up` | answered 2xx | green + 18% halo |
| `.down` | refused, timed out, or non-2xx | red + 18% halo |
| `.unknown` | not probed yet | flat grey, no halo |
| `.not-probed` | has no health endpoint at all | dashed ring, no fill |

Distinguishing the last two is not pedantry. "I have not asked yet" and
"there is nothing to ask" are different facts, and both are different from
"it is down".

### Sparkline

Twenty-four 3px bars (the probe history the console keeps), `on` green / `off` red, 12px tall on a 5px `--panel-3`
baseline. Pure CSS, no canvas, no library. Shows the last couple of minutes
of probes. Cheap history beats an instantaneous truth: it tells you
*flapping* apart from *down*.

### Stat tile

A row of `min-width: 108px` tiles above a list: big tabular number, tiny
uppercase caption. Modifiers `.up`, `.down`, `.accent` colour **the number
only**. Four tiles maximum — this is a summary, not a dashboard.

### Definition list (`.kv`)

`grid-template-columns: max-content 1fr`, `gap: 6px 18px`. Term in
`--text-faint` sans, value in mono. This is the default renderer for "the
record" inside an expanded card. Override to sans on the values that are
prose (see [Type](#type)).

### Table

Uppercase 10.5px faint headers with a `--border` underline; rows separated
by `--border-soft`; last row's border removed. Numerics right-aligned and
tabular. No zebra striping, no row hover — the borders are enough and
striping in a dark palette reads as noise.

### Form

`.form` is a **flex row of `.field` columns**, `align-items: flex-end`, so
labels of different lengths still line up their inputs, and the submit
button sits on the same baseline. `.field.wide { flex: 1 1 220px }` for the
one that should soak up space.

Inputs are `--panel-2` on `--border`, `--radius-sm`. Focus is the accent
border plus a 3px `--accent-ghost` ring — the same ring everywhere, on
every focusable thing:

```css
input:focus, select:focus, textarea:focus,
:where(button, a, [tabindex]):focus-visible {
  outline: none; border-color: var(--accent);
  box-shadow: 0 0 0 3px var(--accent-ghost);
}
```

Textareas are mono, `min-height: 120px`, `resize: vertical` — they hold
JSON.

### File field

Native `<input type="file">`. No dropzone, no library, no drag overlay.
The control sits in a `.field` like any other input; the submit button is
sentence-case and names the destination ("Upload to SFTP location"), not
"Choose file" twice. A `.note` above the form says where the bytes will
land and why a field is required. The toast body is the peer's response
verbatim (path written, byte count, rejection reason).

The console never writes a bind-mount itself. The button is exactly one
`POST` a human could make with `curl --data-binary`.

### Note

A left-bordered wash, used for the sentence that explains what you are
looking at. Default is accent; `.bad` and `.warn` shift it. A note is
*prose*, not a value — it is the place to say why something is read-only,
or why an action is not offered.

### Toast

Bottom-right stack, `--panel-2`, 3px left border in accent / green / red,
title in prose, **body in mono, pre-wrapped, `max-height: 8em`, scrollable**.
The body carries the peer's own message verbatim.

Failures live **12 seconds**, successes **6**. Failures carry the thing
worth reading twice.

The stack needs `aria-live="polite"` (and `role="alert"` for failures) —
otherwise the entire feedback channel of the app is invisible to a screen
reader.

### Modal

Backdrop `rgba(6, 4, 12, .6)` + `blur(3px)`, `display: grid; place-items: center`,
panel `min(620px, 100%)`, `max-height: 86vh`. Head / body / foot; foot is
right-aligned with ghost-cancel then primary-confirm. Closes on backdrop
click and on Escape.

**One trap worth knowing about**, because it cost this repo a commit: the
browser's built-in `[hidden]` rule is a bare attribute selector, so *any*
class that sets `display` outranks it. A `.modal-backdrop { display: grid }`
plus `hidden` leaves the backdrop covering — and blurring — the whole page
from first paint. Every app in this family ships:

```css
[hidden] { display: none !important; }
```

Also give the panel `role="dialog" aria-modal="true"`, move focus into it
on open, and return focus to the trigger on close.

### Buttons

Three kinds and no more:

| Class | Use | Look |
| --- | --- | --- |
| `.btn` | the one primary action in its context | accent gradient, white, soft shadow |
| `.ghost` | everything else | `--panel-2`, `--text-dim`, bordered |
| `.danger` | destructive | transparent, red text, 40% red border |

`.small` shrinks any of them. A card body has **at most one `.btn`**; if
two actions both look primary, one of them is not.

`.danger` is transparent by default and only fills on hover — a delete
button that is a solid red slab pulls the eye to the worst thing on the
screen.

---

## Behaviour contracts

The tokens make it look right. These make it *feel* right.

### 1. Poll, and re-render wholesale

State is small; re-render the active view completely on every change rather
than diffing. Poll every **5s**. But **pause the poll** when:

- a modal is open,
- `document.hidden`,
- the active element is an `INPUT`, `TEXTAREA` or `SELECT`.

Re-rendering replaces the DOM, and doing that under a half-typed form
throws the operator's input away.

And preserve, across every re-render:

- **which cards are open** (state keyed `view:id`),
- **scroll position** (`const y = content.scrollTop; … content.scrollTop = y`).

A page that jumps to the top while you are reading a table is a page you
close.

### 2. Declare actions in markup, run them from one listener

```html
<button class="btn small" data-action="provision" data-id="{id}">Register outlets</button>
```

```js
const ACTIONS = {
  provision: async (d) => { … },
};
```

One delegated handler disables the button, swaps its label to `…`, awaits,
toasts the outcome, restores the label and reloads. Every handler is a
one-liner and every failure surfaces identically. No per-button
`onclick`, no bespoke error handling.

### 3. Show the peer's error verbatim

```js
const msg = (parsed && (parsed.error || parsed.message)) || text || `HTTP ${res.status}`;
```

A control panel that swallows a vendor's 422 and shows "request failed" is
worse than `curl`. Wrong-looking text from the peer is better than
right-looking text from you.

### 4. Report partial success as partial

A bulk action over N things reports **per thing**. "Provisioned with
failures" listing the two MIDs that failed, red, is a correct outcome —
not an error, and not a success.

### 5. Never offer what the backend cannot do

If a vendor has no account-opening endpoint, do not render a disabled
button. Render a `.note.warn` **saying why there is no button**. And make
the API distinguish it: `501`, not `500`. A UI that cannot tell "this
vendor has no such endpoint" from "this call failed" will offer a retry
button forever.

### 6. Degrade to an explanation, never to a blank

Missing credentials are not fatal. Without the signing key, the view still
renders and says which key is missing and what it would have unlocked.
Without the CA, the affected panels show the error rather than
disappearing. An app that refuses to start because one peer's credentials
are missing is worse than one that tells you which.

### 7. Confirm the irreversible, and say what stays

```js
confirm('Delete this merchant? Anything already registered at B4B stays there — the rail has no delete.')
```

The confirm text states the *limit of the undo*, not "are you sure".

### 8. Derive generated data from the record's own id

Anything the UI invents — addresses, reference numbers, contact emails —
is derived deterministically from the record's id, never from a random
source, so a screenshot, a reloaded file and a test fixture agree. And it
is **obviously fake**: `GB00SIM…` accounts, `.test` domains (RFC 2606, so
they can never be mailed), streets nobody can post to.

### 9. Route the view in the hash

`location.hash` is the view. Reload lands where you were; a link to a view
is shareable. Nothing else goes in the URL.

---

## Voice

The copy is half the design.

- **Sentence case** for titles, headings, buttons. Not Title Case.
- **Lowercase for machine states**: `up`, `down`, `unknown`,
  `no health endpoint`. They are values, not sentences.
- Every view has **one sentence** under its title saying what it is *for*,
  and it may be opinionated: "The outlet is what the money cares about."
- Explain the **why** at the point of confusion, not in a docs page. Notes
  and hints exist to say "read-only here because the service loads this
  file at start-up, so an edit that appeared to take effect immediately
  would be a lie."
- Prefer "what it actually does" over "description".
- Empty states carry the next action: *"No merchants yet. Create one — it
  is what every outlet, MID and payout in this lab hangs off."*
- Never bluff. If a number is from a config file rather than the running
  service, label it *what compose.yml ships*, not a live reading.

---

## Checklist for a new app

- [ ] One `index.html`, one `app.css`, one `app.js`, embedded with `//go:embed`.
- [ ] Token block copied verbatim from this document.
- [ ] `[hidden] { display: none !important; }` present.
- [ ] Theme: `data-theme` on `<html>`, OS default, `localStorage` in `try/catch`.
- [ ] Test asserting no asset references an external host.
- [ ] Sidebar + topbar + single scrolling content region; 760px breakpoint.
- [ ] Cards for records; open state keyed `view:id` and preserved across polls.
- [ ] Poll paused on modal / hidden tab / focused field; scroll preserved.
- [ ] `data-action` + one delegated listener + toast, for every action.
- [ ] Peer errors shown verbatim; partial results shown per item.
- [ ] Four health states including *unknown* and *not probed*.
- [ ] Purple decoration, status colour only for status.
- [ ] Contrast ≥ 4.5:1 for every readable string, small caps included.
- [ ] `:focus-visible` ring on everything focusable; card heads keyboard-operable.
- [ ] `aria-live` on the toast stack; `role="dialog"` + focus return on the modal.
- [ ] `prefers-reduced-motion` honoured.
- [ ] Every button is one call to a public API. Nothing is UI-only.

---

## Where the console does not yet match this document

The document is deliberately ahead of the implementation in a few places.
None of these are decisions to revisit — they are work to do:

| Item | Console today |
| --- | --- |
| `--text-faint`, light `--up`, primary-button gradient | still the pre-contrast-fix values |
| `:focus-visible` ring on buttons and nav | inputs only |
| Card head keyboard operation | mouse only |
| `aria-live` toasts, `role="dialog"` modal, focus return | none |
| `prefers-color-scheme` on first paint | hard-coded dark |
| `prefers-reduced-motion` | not honoured |
| Firefox scrollbar (`scrollbar-color`) | `::-webkit-scrollbar` only |

## Changing the system

Edit this file. Then bring the implementations to it — and if a change
invalidates a row in the table above, delete the row. A design document
that describes an app nobody updated is worse than none, because the next
app is built from the document.

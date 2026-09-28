# Tax Form Annotation (TFA) — Specification v1.0.0

TFA is a declarative format for describing a tax form so that any application can print values into its boxes. An annotation file says, for every box on one revision of one form:

- **where** it is (page, rectangle, which copies of the form),
- **what** goes in it (a path into the taxpayer's data set),
- **how** the value is printed (formatting, typography, what to do when it doesn't fit).

It deliberately does **not** say how to render. Implementers use their own code and PDF tooling; this document defines the behaviour precisely enough that two independent implementations print the same characters at the same positions.

| | |
|---|---|
| Format definition | [`annotation/types.go`](annotation/types.go) (Go types, normative field docs) |
| JSON Schema | [`schema/annotation.schema.json`](schema/annotation.schema.json) (generated from the Go types, draft 2020-12) |
| Worked example | [`examples/w2-2026.annotation.json`](examples/w2-2026.annotation.json) (2026 Form W-2, all six copies) |
| Reference implementation | [`annotation/`](annotation/) (resolve + validate) and [`cmd/render`](cmd/render) (test-only PDF writer) |

The key words MUST, SHOULD and MAY are used as in RFC 2119.

---

## 1. Model

Two phases, three inputs:

```
AUTHORING (once per form revision)                RENDERING (every time a form is produced)

 blank IRS PDF ──► extract rects ──► annotate ──►  annotation.json ─┐
                   (cmd/extract)    (source,       blank PDF ───────┼──► renderer ──► filled PDF
                                     format…)      taxpayer data ───┘   (yours)
                         validate + debug render
```

In this repository, `make generate-annotation` extracts the rectangles, `make debug` renders with every box outlined, and `make render` produces the filled form (see the README).

- An **annotation** is static: one per form revision, reused for every taxpayer, never edited after publication (§10).
- The **data** is per taxpayer and can be arbitrarily nested; the annotation reaches into it with paths (§4).
- The **renderer** is generic: it knows nothing about W-2s or 1040s. All form knowledge lives in the annotation.

### Goals

1. Complete enough for real forms: multiple copies per PDF, multiple forms per page, repeating rows, radio-style checkboxes, comb fields, masked identifiers, split dollars/cents.
2. Deterministic: the same inputs produce the same output in any language.
3. Safe by default: problems are reported, never silently printed wrong.

### Non-goals

- **Calculations.** Totals, limits and phase-outs belong to the tax engine. The annotation prints values; it never computes them. (See §12.1.)
- **Mandating a rendering stack or API.** Only the output is specified.

---

## 2. Minimal example

```json
{
  "specVersion": "1.0.0",
  "annotationVersion": 1,
  "form": {
    "id": "irs-w2", "title": "Form W-2 Wage and Tax Statement", "jurisdiction": "US",
    "taxYear": 2026, "revision": "2026",
    "pdf": { "sha256": "61eca7c8…f087", "pageCount": 11 }
  },
  "instances": "$.return.income.wages.w2s[*]",
  "layout": {
    "pageSize": { "width": 612, "height": 792 },
    "copies": [{ "name": "B", "pages": [3] }],
    "slots": [{ "x": 0, "y": 0 }, { "x": 0, "y": -396 }]
  },
  "fields": [
    {
      "id": "box1",
      "label": "1 Wages, tips, other compensation",
      "rect": { "x": 333.2, "y": 708, "width": 118.4, "height": 12 },
      "source": "@.compensation.wages",
      "format": { "type": "currency", "zero": "blank" }
    }
  ]
}
```

This prints every W-2 in the data set onto Copy B, two per page, with box 1 formatted like `98,250.50`, right-aligned.

---

## 3. Document structure

| Key | Required | Description |
|---|---|---|
| `specVersion` | yes | TFA version the file is written against (semver). |
| `annotationVersion` | yes | Revision of this file, starting at 1 (§10). |
| `form` | yes | Identity of the blank form: `id`, `title`, `jurisdiction`, `taxYear`, `revision`, and `pdf` (`sha256`, `pageCount`, optional `source`). |
| `instances` | no | Path selecting the **form instances**: one form is printed per match, e.g. one W-2 per employer (§4.2). Omit for forms printed once per return. |
| `layout` | yes | `pageSize`, `copies`, optional `slots` (§5). |
| `fields` | yes | Every box on the form, in reading order (§6). Repeating rows are fields with a `group` (§5.4). |

Unknown keys anywhere else are errors. This catches typos (`"sorce"`) at authoring time. Implementations SHOULD decode strictly; the JSON Schema sets `additionalProperties: false`.

---

## 4. Referencing data

### 4.1 Paths

Paths are **RFC 9535 JSONPath** queries, with one addition:

| Prefix | Evaluated against | Example |
|---|---|---|
| `$` | the data root | `$.return.taxpayer.ssn` |
| `@` | the **current item** | `@.employer.address.city` |

An `@` path is evaluated by replacing the leading `@` with `$` and running the query against the current item. Everything RFC 9535 offers is available: nested members, indexes (`@.items[0]`), wildcards, and filters (`@.codes[?@.code == 'DD'].amount`).

Why JSONPath: it is standardized (RFC 9535, 2024), has implementations in every mainstream language, and supports filters. Tax data is often selected *by attribute* ("the box 12 entry with code DD") rather than by position. JSON Pointer (RFC 6901) cannot filter; a custom DSL would be one more thing for every implementer to write.

### 4.2 Instances and the current item

A person can have several of the same form: a W-2 from each employer, a 1099-INT from each bank. `instances` selects them, and the form is printed once per match. `@` is the current item:

1. For top-level fields: the current **form instance**, meaning each node matched by `instances` (e.g. each W-2). Without `instances` (forms printed once per return, like the 1040), the data root.
2. Inside a group: the current **row**, meaning each node matched by the group's `instances` (§5.4).

Each form sees only its own item, so a value can never land on another employer's form.

Using `@` keeps annotations short and lets the same form be printed for any instance. Absolute `$` paths remain available for values shared across instances.

### 4.3 Resolution rules

| Situation | Result |
|---|---|
| Path matches nothing, or matches JSON `null` | Value is *missing*: the box is left blank. If `required`, report `missing_required`. |
| Path matches exactly one node | That node is the value. |
| Path matches several nodes on a `text`, `comb` or `checkbox` field | Error `invalid_path`; box left blank. |
| Path matches several nodes on a `multiline` field | Each node is one line. Array nodes are flattened, one element per line. |

### 4.4 Templates

A field may use `template` instead of `source`. That's plain text with embedded paths in braces:

```json
"template": "{@.employer.name}\n{@.employer.address.line1}\n{@.employer.address.line2}\n{@.employer.address.city}, {@.employer.address.state} {@.employer.address.zip}"
```

- Each placeholder MUST resolve to a single scalar (or be missing, which substitutes `""`).
- A line whose placeholders are **all** empty is dropped, so a missing `line2` does not leave a gap.
- Each resulting line is trimmed of surrounding spaces.
- Templates produce text; of the format options, only `upper` applies.

### 4.5 Value types

Data is JSON. Implementations MUST keep numbers exact. In Go, decode with `UseNumber`; in JavaScript or Python, parse amounts as decimals. **Monetary amounts MAY be JSON numbers or decimal strings** (`"98250.50"`). Strings with grouping separators (`"52,000"`) are not numbers and produce `format_error`: the data contract is strict so ambiguity cannot reach a tax form.

---

## 5. Geometry

### 5.1 Coordinates

- Units: **PDF points** (1/72 inch).
- Origin: **bottom-left** corner of the page. This is PDF user space, the same space AcroForm widget `/Rect`s use, so rectangles can be copied from a fillable PDF unchanged.
- `rect` = `{x, y, width, height}`: `(x, y)` is the rectangle's bottom-left corner.
- `layout.pageSize` gives the page size (US Letter: 612 × 792). Every placed rectangle MUST lie within it.

A rectangle, not a point, because the renderer needs the box's extent for right alignment, vertical centering, wrapping, shrink-to-fit, comb cells and overflow detection.

### 5.2 Copies

One PDF often contains several copies of the same form (W-2: A, 1, B, C, 2, D), each on its own page with identical box positions.

```json
"copies": [
  { "name": "A", "description": "For Social Security Administration", "pages": [1] },
  { "name": "B", "description": "To Be Filed With Employee's FEDERAL Tax Return", "pages": [3] }
]
```

- `pages` lists 0-based PDF page indexes in order. A two-page form (1040) has `"pages": [0, 1]`.
- A field's `page` (default 0) indexes into its copy's `pages`, so fields are written once and apply to every copy.
- `copies` on a field or group restricts it to some copies. On the W-2 the VOID checkbox exists only on Copies A and D.

### 5.3 Slots (several instances per page)

Some forms print more than one instance per sheet: two W-2s per page, three 1099s per page. `slots` lists the offset of each instance position. Field rectangles are authored for the first slot.

```json
"slots": [{ "x": 0, "y": 0 }, { "x": 0, "y": -396 }]
```

```
 page (612 × 792)
┌───────────────────────────┐
│ slot 0: instance 0, 2, …  │  rect as authored
├───────────────────────────┤
│ slot 1: instance 1, 3, …  │  rect + (0, −396)
└───────────────────────────┘
```

Instances fill slots in order. When there are more instances than slots, the copy's pages are repeated as extra **sheets**. Unused slots on the last sheet stay blank.

### 5.4 Groups (repeating rows)

A field with a **`group`** holds other fields and repeats them once per item of a list. W-2 box 12 (four code/amount rows), boxes 15–20 (two state rows), box 14b (two cells side by side), a 1040 dependents table. Because a group is a field, it sits in `fields` at its natural place: box 12 comes between box 11 and box 13, as on the paper.

```json
{
  "id": "box12",
  "label": "12a-12d See instructions for box 12",
  "group": {
    "items": "@.codedItems",
    "rows": [{ "x": 0, "y": 0 }, { "x": 0, "y": -24 }, { "x": 0, "y": -48 }, { "x": 0, "y": -72 }],
    "fields": [
      { "id": "code",   "rect": { "x": 462.8, "y": 588, "width": 25.8, "height": 12 }, "source": "@.code" },
      { "id": "amount", "rect": { "x": 490.6, "y": 588, "width": 83.4, "height": 12 }, "source": "@.amount",
        "format": { "type": "currency" } }
    ]
  }
}
```

- `items` is the list that fills the rows, one item per row. (The top-level `instances` picks which *forms* to print; a group's `items` picks which list *inside one form* fills its rows: box 12 uses `@.codedItems`, the state rows `@.stateAndLocal`.) If the path selects a single array, the array's elements are the items; otherwise each selected value is an item. Filters work: `"@.codedItems[?@.code != 'DD']"`.
- `rows` gives one offset per printed row. Offsets are explicit, not a fixed step, so irregular tables work. Horizontal repetition is simply an `x` offset.
- Inside the group, `@` is the current item; an item that is a plain value (like `"102"`) is addressed as `"@"`.
- A field with a group takes only `id`, `label`, `page`, `copies` and `group`. It has no `rect`, `source` or `format` of its own; those belong on its inner fields.
- Inner fields inherit `page` and `copies` from the group's field, and must not set them.
- **Groups cannot be nested.** A group inside a group is rejected (`invalid_group`); tables within tables are out of scope.
- More items than rows: the first `len(rows)` are printed and `group_overflow` (error) is reported. The IRS's answer for the W-2 (issue an additional W-2) is a policy decision for the caller, not the annotation. See §13.
- Diagnostics and debug output name rows as `box12[2].amount`.

---

## 6. Fields

| Key | Default | Description |
|---|---|---|
| `id` | required | Unique in the annotation (in the group, for group fields). `[A-Za-z0-9_.-]`. |
| `label` | | Box label as printed, e.g. `"12a See instructions for box 12"`. Documentation and diagnostics. |
| `kind` | `text` | `text`, `multiline`, `comb` or `checkbox`. |
| `page` | 0 | Index into the copy's `pages`. |
| `copies` | all | Restrict to these copy names. |
| `rect` | required (except with `group`) | §5.1. |
| `source` / `template` | exactly one | §4. Checkboxes require `source`. |
| `checkedValue` | | Checkbox only (§6.4). |
| `cells` | | Comb only: number of character cells. |
| `format` | text | How the value is printed (§7). |
| `style` | | Typography (§8); unset keys take their built-in defaults. |
| `overflow` | `shrink` | `shrink` or `error` (§8.4). |
| `group` | | `{ items, rows, fields }`: makes this field repeating rows (§5.4). |
| `required` | false | Report `missing_required` when the value is missing. |
| `meta` | | Free-form extension data (e.g. an e-file XPath). |

### 6.1 `text`
One line. Placed per §8.2.

### 6.2 `multiline`
Paragraphs come from embedded `\n`, template lines, or multiple nodes (§4.3). Each paragraph is word-wrapped to the box width. Top-aligned by default.

### 6.3 `comb`
One character per equal-width cell (bank routing numbers, some state forms). Characters fill from the left, or from the right when `align` is `right`. Comb fields never shrink: extra characters are truncated with a warning, or with `overflow: error` the box is left blank.

### 6.4 `checkbox`
Prints `style.mark` (default `X`), centered in the box, when the value is **checked**:

- without `checkedValue`: the value is JSON `true`;
- with `checkedValue`: the value equals it, compared as text.

A radio-style set (filing status) is several checkbox fields on the same source:

```json
{ "id": "fs_single", "kind": "checkbox", "source": "$.return.filingStatus", "checkedValue": "single", "rect": {…} },
{ "id": "fs_mfj",    "kind": "checkbox", "source": "$.return.filingStatus", "checkedValue": "married_filing_jointly", "rect": {…} }
```

### 6.5 Fields with a `group`
Repeat their inner fields once per row. See §5.4.

---

## 7. Formatting

A format converts one value to the printed string. Options that don't apply to the `type` are invalid.

| `type` | Input | Options | Example output |
|---|---|---|---|
| `text` (default) | string, number, boolean | `upper`: `true` prints capital letters | `DD` |
| `currency` | number or decimal string | `decimals` (2), `negative` (`minus`/`parens`), `zero` (`show`/`blank`), `part` | `98,250.50`, `(1,234.00)` |
| `number` | number or decimal string | same as currency, `decimals` defaults to 0 | `1,234,567` |
| `ssn` | 9 digits; `-` and spaces ignored | `mask` | `123-45-6789`, `XXX-XX-6789` |
| `ein` | 9 digits; `-` and spaces ignored | `mask` | `12-3456789` |
| `date` | ISO 8601 `YYYY-MM-DD` | `pattern` using `YYYY` `YY` `MM` `DD` (default `MM/DD/YYYY`) | `04/15/2026` |

Rules:

- **Rounding** is half away from zero on the **exact decimal value**. Binary floating point MUST NOT be used: `0.125` → `0.13`, which `float64` gets wrong.
- A result that rounds to zero never carries a sign (`-0.001` → `0.00`). With `zero: "blank"` it prints nothing.
- **`part`** supports forms with separate dollars and cents boxes: `integer` prints `-1,234` (the sign stays with the dollars), `fraction` prints `57`. Both come from the *same* rounded value, so the two boxes are always consistent.
- **`mask`** (`ssn`, `ein`) replaces all but the last `visible` digits with `char` (default `X`), optionally only on some `copies`. The W-2 example masks the SSN on employee copies B, C and 2 (IRS truncation rules) and never masks the EIN, which the IRS does not allow.
- An input that doesn't match its type (`"12-345"` as an SSN, `"abc"` as currency) leaves the box **blank** and reports `format_error`. Printing a malformed identifier on a tax form is worse than printing nothing.

Formats are always written inline on the field. The W-2 example repeats `{"type": "currency", "zero": "blank"}` on every money box: more verbose, but each box reads completely on its own (§12.9).

---

## 8. Typography and placement

### 8.1 Style

A field's `style` may set any of these keys. Every key it leaves unset takes the built-in default below; there is no annotation-wide defaults block (§12.9).

| Key | Default | Description |
|---|---|---|
| `size` | 10 | Points. |
| `minSize` | 6 (or `size`, if smaller) | Smallest size `shrink` may use. |
| `align` | `right` for currency/number, else `left` | `left`, `center`, `right`. |
| `valign` | `top` for multiline, else `middle` | `top`, `middle`. |
| `padding` | 2 | Inner spacing on every side, points. |

Fixed for every field, not configurable: the font is **Courier**, multiline lines are **1.15 × size** apart, and a checked checkbox prints **`X`**. Commas are always used in amounts, and no currency symbol is printed (IRS boxes don't use one).

**Why only Courier in v1.** The IRS recommends Courier for machine-scanned copies, and it's monospaced: every glyph is 0.600 em wide. That makes every measurement below plain arithmetic, identical in every language, with no font files or metric tables. Proportional fonts are a planned extension (§13).

Standard fonts cover printable Latin-1 (U+0020–U+007E, U+00A0–U+00FF). Other characters are replaced with `?` and reported as `unsupported_char`.

### 8.2 Placement formulas (normative)

With `s` = font size, `p` = padding, box `(X, Y, W, H)`, and Courier metrics `cw = 0.600·s` (glyph width), `ch = 0.562·s` (cap height), `d = 0.157·s` (descent):

```
text width     w   = characters × cw
available      A   = W − 2p
x (left)           = X + p
x (right)          = X + W − p − w
x (center)         = X + (W − w) / 2

first baseline for n lines, lh = 1.15 · s:
  top              = Y + H − p − ch
  middle           = Y + (H + ch + (n−1)·lh) / 2 − ch
each further line  = previous − lh
```

- **multiline:** wrap each paragraph at `floor(A / cw)` characters, breaking at spaces; a word longer than a line is hard-broken. Lines that fit: `1 + floor((H − 2p − ch) / lh)`.
- **comb:** `cell = W / cells`; character *i* is drawn at `x = X + (start + i)·cell + (cell − cw)/2`, where `start` is 0, or `cells − length` when right-aligned.
- **checkbox:** the mark is centered horizontally, baseline per `middle`.

### 8.3 Validation of style

Validation warns (`text_too_tall`) when `(0.562 + 0.157) · minSize > H − 2p`, meaning the text cannot fit the box vertically.

### 8.4 Overflow

When text is wider than `A` (single line) or needs more lines than fit (multiline):

| `overflow` | Behaviour |
|---|---|
| `shrink` (default) | Reduce size in 0.5pt steps while it doesn't fit and the next step is ≥ `minSize`. If it still doesn't fit, cut to `floor(A / cw)` characters (or the first lines that fit) and warn. To cut without shrinking, set `minSize` equal to `size`. |
| `error` | Print nothing; report an error. Use it for identifiers, where a truncated value is a wrong value. |

---

## 9. Rendering algorithm (normative)

```
instances ← annotation.instances ? select(annotation.instances, data) : [data]
slots     ← layout.slots or [{0,0}]
for copy in layout.copies:                                   # in order
  for sheet in 0 … ceil(len(instances)/len(slots)) − 1:
    for (pageIndex, pdfPage) in enumerate(copy.pages):
      output page ← a fresh copy of blank page pdfPage
      for (slotIndex, offset) in enumerate(slots):
        instance ← instances[sheet·len(slots) + slotIndex]  (stop when exhausted)
        for field in fields where field.page = pageIndex and applies to copy:   # in order
          if field has a group:
            items ← selectItems(field.group.items, instance)
            for (i, item) in first len(field.group.rows) of items:
              for inner in field.group.fields:
                draw(inner, @ = item, shift = offset + field.group.rows[i])
          else:
            draw(field, @ = instance, shift = offset)

draw(field): value → format (§7) → fit and place (§8) → text runs
```

The output page count is `Σ len(copy.pages) × number of sheets`. The W-2 example with three W-2s produces 6 copies × 2 sheets = 12 pages. Renderers SHOULD remove the blank PDF's interactive form fields from the output, since they would sit on top of the printed text.

Errors are **local**: a bad value blanks its own box and is reported, and the rest of the form still renders. The caller decides whether errors block delivery (the reference renderer's `-strict` flag).

---

## 10. Versioning

Three independent axes:

| Axis | Field | Changes when |
|---|---|---|
| The format | `specVersion` (semver) | This specification changes. |
| The form | `form.id` + `form.taxYear` + `form.revision`, pinned by `form.pdf.sha256` | The IRS publishes a new form. |
| The annotation | `annotationVersion` | Someone fixes or improves this annotation. |

Rules:

1. **Published annotation files are immutable.** A correction is a new file with `annotationVersion + 1`. Old files are kept, never deleted.
2. **Select by form and year.** Printing a 2026 return uses a 2026 annotation, even in 2030. Among those, use the highest `annotationVersion` unless one is pinned.
3. **The PDF hash MUST match.** Renderers refuse to draw on a PDF whose SHA-256 differs from `form.pdf.sha256`. That prevents 2026 coordinates landing on a 2025 PDF, a failure that would otherwise look fine and print every value slightly wrong.
4. **`specVersion` is semver.** A renderer MUST reject an unknown *major* version. Minor versions only add optional keys; validate a file against the schema of its own `specVersion`.
5. **Record provenance.** Renderers SHOULD record `form.id`, `form.taxYear`, `annotationVersion` and the PDF hash in the output's metadata, so any printed form can be reproduced and audited.

Suggested storage layout (not required):

```
annotations/irs-w2/2025/v1.json
annotations/irs-w2/2026/v1.json
annotations/irs-w2/2026/v2.json   ← correction
```

---

## 11. Diagnostics

Validation and rendering report diagnostics instead of failing outright. Each has a `severity` (`error` or `warning`), a `code`, the form `instance` (or −1), the `field`, and a message.

| Code | Severity | Raised when |
|---|---|---|
| `missing_required` | error | A `required` value is missing or null. |
| `format_error` | error | A value doesn't match its format (malformed SSN, non-decimal amount, bad date). |
| `invalid_path` | error | A path is malformed, or matched several values where one was expected. |
| `group_overflow` | error | A group has more items than rows. |
| `overflow` | warning / error | Text didn't fit (truncated → warning; `overflow: error` → error). |
| `unsupported_char` | warning | A character outside Latin-1 was replaced with `?`. |
| `no_instances` | warning | `instances` matched nothing. |
| `pdf_mismatch` | error | The PDF's hash differs from `form.pdf.sha256`. |
| `unsupported_spec` | error | Unknown major `specVersion`. |
| `off_page` / `overlap` | error | A placed rectangle (any slot, any row) leaves the page or overlaps another. |
| `text_too_tall` | warning | The box is too short for its minimum font size. |
| `invalid_*`, `duplicate_id`, `unknown_copy` | error | Structural problems in the annotation. |

---

## 12. Design decisions

### 12.1 No calculations in the annotation
The annotation references values; it never derives them. Box 3 is capped at the Social Security wage base, box 1 excludes 401(k) deferrals: that logic is the tax engine's job, where it's versioned and tested with the rest of the tax law. An annotation that can compute becomes a second, untested tax engine. (Formatting `part` is not a calculation: it only displays a portion of one value.)

### 12.2 A rectangle per box, copied from the PDF
Rectangles use the PDF's native coordinate space, so they come straight from AcroForm widgets (`make generate-annotation`, which runs `cmd/extract`) instead of being measured. Humans add meaning (`source`, `format`); machines provide geometry.

### 12.3 JSONPath (RFC 9535) with a current item
The standard gives filters and libraries everywhere. The single addition, `@` = the current item, keeps paths short and makes one annotation work for every instance and every group row.

### 12.4 Declarative, named formatters
`{"type": "currency", "negative": "parens"}` is described by this spec, not by a printf or locale pattern whose behaviour differs between languages and runtimes. That's what makes output reproducible across implementations.

### 12.5 Monospace typography
It trades typographic polish for determinism and zero dependencies. It matches IRS guidance for scannable forms.

### 12.6 Copies, slots and rows share one mechanism
All three are *offsets applied to authored rectangles*. Fields are written once, whether they're repeated across six copies, two instances per page, or four box-12 rows.

### 12.7 Fail locally, report everything
One bad value blanks one box and produces a diagnostic. The caller, not the renderer, decides whether a form with errors may be delivered.

### 12.8 JSON, with Go types as the source of truth
JSON is language-neutral and easy to diff and review. The Go types define the format, and the JSON Schema is generated from them, so the two cannot drift. Strict decoding rejects unknown keys.

### 12.9 One way to say each thing
There is no annotation-wide `defaults` block and no named, reusable formats. Both were convenience layers that duplicated something already present (built-in defaults; inline formats) and forced readers to look in two places. Annotations are small, authored once and loaded rarely, so readability beats brevity: every field states its own format, and anything it doesn't state has exactly one default, defined in this spec.

---

## 13. Limitations and future enhancements

- **Visual annotation editor:** draw or adjust rectangles on the PDF, pick paths from a sample data tree, with live preview. `make generate-annotation` and `make debug` are the command-line precursors.
- **Extraction (the reverse direction):** use the same rectangles to OCR values from uploaded forms (align the scan to the template, crop each box, read). One annotation would serve both printing and data intake.
- **Continuation handling:** when a group overflows (a fifth box 12 code), automatically emit an additional form or a "see attached statement" page per the form's instructions.
- **Proportional fonts and Unicode:** publish metric tables (e.g. Helvetica AFM widths) or allow embedded fonts with declared metrics.
- **Conditional printing:** a small `when` predicate for boxes that print only under a condition, without growing into a calculation language.
- **Joins and parameterized paths:** e.g. resolve a W-2's `recipient: "spouse"` to the spouse record, or reach the instance from inside a group row. Today the data must be shaped so each value is reachable by a plain path.
- **Year-over-year migration:** diff the widget rectangles of consecutive revisions and carry `source`/`format` forward, so only moved boxes need a human.
- **More field kinds:** 2D barcodes (several state forms require them), signature and date blocks, images.
- **E-file mapping:** a `meta.mef` XPath per field would let one annotation also drive IRS MeF XML.
- **Golden-image regression tests** for every annotation in CI, plus schema-freshness checks.

Notes on the W-2 example: box 9 is intentionally not annotated (it is blank on the 2026 form), and the employer's EIN is never masked because the IRS does not permit truncating it. The IRS also states that Copy A downloaded from irs.gov is not machine-scannable (the official Copy A is printed in red ink) and employers file Copy A electronically with the SSA. The rendered Copy A in this repository demonstrates the format, not a filing path.

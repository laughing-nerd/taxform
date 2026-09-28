# Tax Form Annotation (TFA)

A data structure for annotating U.S. tax forms: where each box is, which value from a (deeply nested) data set goes in it, and how that value is formatted. With it, anyone can print values onto the official PDF using their own code.

**Start with [SPEC.md](SPEC.md).**

| Deliverable | Where |
|---|---|
| Specification | [`SPEC.md`](SPEC.md) |
| Data structure (Go types, normative docs) | [`annotation/types.go`](annotation/types.go) |
| JSON Schema (generated from the types) | [`schema/annotation.schema.json`](schema/annotation.schema.json): for tools (validators, editor autocomplete) in any language. To read the format, use `types.go` or SPEC.md instead. |
| Worked example: 2026 Form W-2, all 6 copies | [`examples/w2-2026.annotation.json`](examples/w2-2026.annotation.json) |
| Sample data (3 W-2s, nested) | [`examples/taxpayer.json`](examples/taxpayer.json), [`examples/taxpayer-bad.json`](examples/taxpayer-bad.json) |
| Rendered output | [`output/w2-filled.pdf`](output/w2-filled.pdf), [`output/w2-debug.pdf`](output/w2-debug.pdf) (box outlines), [`output/w2-bad.pdf`](output/w2-bad.pdf) (broken data, problem boxes left blank) |

The Go code (`annotation/`, `cmd/`) is a **reference implementation used to prove the spec**. It is not a required part of adopting the format.

## Try it

Requires Go 1.25+ and `make`. Run `make` on its own to list every target.

```sh
make render    # fill the W-2 with the sample data            → output/w2-filled.pdf
make debug     # same, with every box outlined and labelled   → output/w2-debug.pdf
make bad       # broken data: malformed SSN, "52,000", 5 box-12 codes,
               # non-Latin name, overlong text; each reported → output/w2-bad.pdf
make demo      # all three
make test      # run the tests
```

`render` checks the annotation against the blank PDF first and refuses to print if its SHA-256 doesn't match.

Authoring:

```sh
make generate-annotation                         # box rectangles of Copy B → drafts/fw2-page3.fields.json
make generate-annotation PDF=form.pdf PAGE=2     # any fillable PDF, any page
make generate-schema                             # after changing annotation/types.go
```

`drafts/` is git-ignored: extracted rectangles are a starting point for a new annotation, not a finished one. Each target is a one-line `go run` command, so see the Makefile to run the tools directly.

## Layout

```
Makefile        every command below, as make targets
annotation/     the format (types.go, constants.go), formatting, resolution, validation, tests
cmd/render      test-only renderer: annotation + data + blank PDF → filled PDF
cmd/extract     authoring helper: AcroForm rectangles → skeleton fields
cmd/schema      generates the JSON Schema from the Go types
schema/         the generated JSON Schema
examples/       blank IRS W-2 (2026), its annotation, sample data
output/         rendered results
```

`examples/fw2.pdf` is the official 2026 Form W-2 from https://www.irs.gov/pub/irs-pdf/fw2.pdf (SHA-256 pinned in the annotation).

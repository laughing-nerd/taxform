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
| Rendered output | [`output/w2-filled.pdf`](output/w2-filled.pdf), [`output/w2-debug.pdf`](output/w2-debug.pdf) (box outlines) |

The Go code (`annotation/`, `cmd/`) is a **reference implementation used to prove the spec**. It is not a required part of adopting the format.

## Try it

Requires Go 1.25+.

```sh
# Render the filled form. It checks the annotation against the PDF first and
# refuses to print on a mismatch. Add -debug to outline every box with its id.
go run ./cmd/render -annotation examples/w2-2026.annotation.json -data examples/taxpayer.json \
  -pdf examples/fw2.pdf -out output/w2-filled.pdf

# See what bad data produces: malformed SSN, "52,000", 5 box-12 codes, non-Latin name, overlong text
go run ./cmd/render -annotation examples/w2-2026.annotation.json -data examples/taxpayer-bad.json \
  -pdf examples/fw2.pdf -out output/w2-bad.pdf

# Authoring: dump a fillable PDF's box rectangles as skeleton fields
go run ./cmd/extract -pdf examples/fw2.pdf -page 3

# Tests; regenerate the schema after changing annotation/types.go
go test ./...
go run ./cmd/schema > schema/annotation.schema.json
```

## Layout

```
annotation/     the format (types.go), formatting, resolution, validation, tests
cmd/render      test-only renderer: annotation + data + blank PDF → filled PDF
cmd/extract     authoring helper: AcroForm rectangles → skeleton fields
cmd/schema      generates the JSON Schema from the Go types
examples/       blank IRS W-2 (2026), its annotation, sample data
output/         rendered results
```

`examples/fw2.pdf` is the official 2026 Form W-2 from https://www.irs.gov/pub/irs-pdf/fw2.pdf (SHA-256 pinned in the annotation).

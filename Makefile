PDF    := examples/fw2.pdf
PAGE   := 3
ANN    := examples/w2-2026.annotation.json
DATA   := examples/taxpayer.json
OUT    := output
DRAFTS := drafts

# Draft file name follows the PDF and page: examples/fw2.pdf, page 3 -> drafts/fw2-page3.fields.json
DRAFT  := $(DRAFTS)/$(basename $(notdir $(PDF)))-page$(PAGE).fields.json

.PHONY: help generate-schema generate-annotation render debug bad demo test

## regenerate schema/annotation.schema.json from annotation/types.go
generate-schema:
	@go run ./cmd/schema > schema/annotation.schema.json
	@echo "wrote schema/annotation.schema.json"

## draft fields (box rectangles) from one PDF page, into drafts/
generate-annotation:
	@mkdir -p $(DRAFTS)
	@go run ./cmd/extract -pdf $(PDF) -page $(PAGE) > $(DRAFT)
	@echo "wrote $(DRAFT)"

## fill the W-2 with the sample data -> output/w2-filled.pdf
render:
	go run ./cmd/render -annotation $(ANN) -data $(DATA) -pdf $(PDF) -out $(OUT)/w2-filled.pdf

## run the tests
test:
	go test ./...

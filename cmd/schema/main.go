package main

import (
	"encoding/json"
	"os"

	"github.com/invopop/jsonschema"

	"taxform/annotation"
)

func main() {
	r := &jsonschema.Reflector{ExpandedStruct: true, DoNotReference: false}
	if err := r.AddGoComments("taxform", "./annotation"); err != nil {
		panic(err)
	}
	s := r.Reflect(&annotation.Annotation{})
	s.ID = "https://example.com/tax-form-annotation/1.0.0/annotation.schema.json"
	s.Title = "Tax Form Annotation " + annotation.SpecVersion
	out, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		panic(err)
	}
	// Put a note for humans on the very first line, using JSON Schema's
	// standard "$comment" keyword (the library would write it further down).
	note := `{
  "$comment": "Generated from annotation/types.go by cmd/schema; do not edit. This file is for tools (validators, editor autocomplete). For humans: read annotation/types.go or SPEC.md.",`
	os.Stdout.Write(append([]byte(note), out[1:]...)) // out[1:] drops out's own opening "{"
	os.Stdout.WriteString("\n")
}

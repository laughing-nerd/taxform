package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"

	"taxform/annotation"
)

func main() {
	annPath := flag.String("annotation", "", "annotation JSON file")
	dataPath := flag.String("data", "", "data JSON file")
	pdfPath := flag.String("pdf", "", "blank PDF the annotation was authored for")
	out := flag.String("out", "output/filled.pdf", "output PDF")
	debug := flag.Bool("debug", false, "outline every box and label it with its field id")
	strict := flag.Bool("strict", false, "exit non-zero when resolving reports errors")
	flag.Parse()
	if *annPath == "" || *dataPath == "" || *pdfPath == "" {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(*annPath, *dataPath, *pdfPath, *out, *debug, *strict); err != nil {
		fmt.Fprintln(os.Stderr, "render:", err)
		os.Exit(1)
	}
}

func run(annPath, dataPath, pdfPath, out string, debug, strict bool) error {
	af, err := os.Open(annPath)
	if err != nil {
		return err
	}
	defer af.Close()
	a, err := annotation.Decode(af)
	if err != nil {
		return fmt.Errorf("%s: %w", annPath, err)
	}
	blank, err := os.ReadFile(pdfPath)
	if err != nil {
		return err
	}
	if errs := errorsOnly(annotation.Validate(a, blank)); len(errs) > 0 {
		for _, d := range errs {
			fmt.Fprintln(os.Stderr, d)
		}
		return fmt.Errorf("annotation is invalid for this PDF; refusing to render")
	}
	df, err := os.Open(dataPath)
	if err != nil {
		return err
	}
	defer df.Close()
	data, err := annotation.DecodeData(df)
	if err != nil {
		return fmt.Errorf("%s: %w", dataPath, err)
	}

	pages, diags := annotation.Resolve(a, data)
	for _, d := range diags {
		fmt.Fprintln(os.Stderr, d)
	}
	if len(pages) == 0 {
		return fmt.Errorf("nothing to render")
	}
	if err := write(blank, pages, out, debug); err != nil {
		return err
	}
	// Provenance (SPEC.md §10 rule 5): enough to reproduce this output.
	props := map[string]string{
		"TFAForm":              fmt.Sprintf("%s %d rev %s", a.Form.ID, a.Form.TaxYear, a.Form.Revision),
		"TFAAnnotationVersion": strconv.Itoa(a.AnnotationVersion),
		"TFASpecVersion":       a.SpecVersion,
		"TFASourcePDFSHA256":   a.Form.PDF.SHA256,
	}
	if err := api.AddPropertiesFile(out, "", props, model.NewDefaultConfiguration()); err != nil {
		return err
	}
	fmt.Printf("wrote %s (%d pages, %d diagnostics)\n", out, len(pages), len(diags))
	if strict && len(errorsOnly(diags)) > 0 {
		return fmt.Errorf("%d errors (strict mode)", len(errorsOnly(diags)))
	}
	return nil
}

func errorsOnly(ds []annotation.Diagnostic) []annotation.Diagnostic {
	var out []annotation.Diagnostic
	for _, d := range ds {
		if d.Severity == "error" {
			out = append(out, d)
		}
	}
	return out
}

// write copies the needed blank pages (duplicating them when there are more
// instances than slots), removes the interactive form fields, and appends a
// content stream with the text to each page.
func write(blank []byte, pages []annotation.Page, out string, debug bool) error {
	conf := model.NewDefaultConfiguration()
	sel := make([]string, len(pages))
	for i, p := range pages {
		sel[i] = strconv.Itoa(p.Source + 1)
	}
	var collected bytes.Buffer
	if err := api.Collect(bytes.NewReader(blank), &collected, sel, conf); err != nil {
		return err
	}
	ctx, err := api.ReadValidateAndOptimize(bytes.NewReader(collected.Bytes()), conf)
	if err != nil {
		return err
	}
	delete(ctx.RootDict, "AcroForm") // our text replaces the fillable fields

	// One font for everything: Courier, built into every PDF viewer, so no
	// font file needs embedding. Pages refer to it by the name "TFA0".
	courier, err := ctx.IndRefForNewObject(types.Dict{
		"Type": types.Name("Font"), "Subtype": types.Name("Type1"),
		"BaseFont": types.Name("Courier"), "Encoding": types.Name("WinAnsiEncoding"),
	})
	if err != nil {
		return err
	}

	for i, p := range pages {
		d, _, _, err := ctx.PageDict(i+1, true)
		if err != nil {
			return err
		}
		delete(d, "Annots")
		if err := addFont(ctx, d, *courier); err != nil {
			return err
		}
		if err := wrapContents(ctx, d, content(p, debug)); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	return api.WriteContextFile(ctx, out)
}

// addFont registers the Courier font on a page under the name "TFA0".
func addFont(ctx *model.Context, page types.Dict, courier types.IndirectRef) error {
	res, err := ctx.DereferenceDict(page["Resources"])
	if err != nil {
		return err
	}
	if res == nil {
		res = types.Dict{}
		page["Resources"] = res
	}
	fd, err := ctx.DereferenceDict(res["Font"])
	if err != nil {
		return err
	}
	if fd == nil {
		fd = types.Dict{}
		res["Font"] = fd
	}
	fd["TFA0"] = courier
	return nil
}

// wrapContents sets Contents to [q, original..., Q + ours]. Duplicated pages
// share their original stream, so it must never be modified in place; the
// q/Q pair isolates our drawing from any graphics state the page leaves set.
func wrapContents(ctx *model.Context, page types.Dict, ours []byte) error {
	newStream := func(b []byte) (types.IndirectRef, error) {
		sd, err := ctx.NewStreamDictForBuf(b)
		if err != nil {
			return types.IndirectRef{}, err
		}
		if err := sd.Encode(); err != nil {
			return types.IndirectRef{}, err
		}
		ref, err := ctx.IndRefForNewObject(*sd)
		return *ref, err
	}
	open, err := newStream([]byte("q\n"))
	if err != nil {
		return err
	}
	closeAndDraw, err := newStream(append([]byte("Q\n"), ours...))
	if err != nil {
		return err
	}
	arr := types.Array{open}
	switch c := page["Contents"].(type) {
	case types.Array:
		arr = append(arr, c...)
	case nil:
	default:
		arr = append(arr, c)
	}
	page["Contents"] = append(arr, closeAndDraw)
	return nil
}

// content builds the PDF drawing commands for one page. The commands used:
//
//	r g b RG / r g b rg   set the line / fill colour
//	w                     set the line width
//	x y w h re S          draw a rectangle outline
//	BT … ET               begin / end a piece of text
//	/TFA0 size Tf         use the Courier font at this size
//	1 0 0 1 x y Tm        move to x, y (where the text's baseline starts)
//	(text) Tj             draw the text
func content(p annotation.Page, debug bool) []byte {
	var b strings.Builder
	if debug {
		b.WriteString("0.85 0.1 0.1 RG 0.85 0.1 0.1 rg 0.4 w\n") // thin red lines and labels
		for _, bx := range p.Boxes {
			r := bx.Rect
			fmt.Fprintf(&b, "%.2f %.2f %.2f %.2f re S\n", r.X, r.Y, r.Width, r.Height)
			fmt.Fprintf(&b, "BT /TFA0 3.5 Tf 1 0 0 1 %.2f %.2f Tm (%s) Tj ET\n", r.X+0.5, r.Y+r.Height+0.8, pdfString(bx.Field))
		}
	}
	b.WriteString("0 g\n") // black text
	for _, t := range p.Texts {
		fmt.Fprintf(&b, "BT /TFA0 %.2f Tf 1 0 0 1 %.2f %.2f Tm (%s) Tj ET\n", t.Size, t.X, t.Y, pdfString(t.Text))
	}
	return []byte(b.String())
}

// pdfString encodes text as a WinAnsi literal string. Resolve guarantees
// every rune is printable Latin-1, which WinAnsi encodes byte-for-byte.
func pdfString(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '(', ')', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteByte(byte(r))
		}
	}
	return b.String()
}

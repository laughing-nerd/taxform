package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"sort"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"

	"taxform/annotation"
)

func main() {
	pdfPath := flag.String("pdf", "", "fillable PDF")
	page := flag.Int("page", 0, "0-based page index")
	flag.Parse()
	if *pdfPath == "" {
		flag.Usage()
		os.Exit(2)
	}
	fields, err := extract(*pdfPath, *page)
	if err != nil {
		fmt.Fprintln(os.Stderr, "extract:", err)
		os.Exit(1)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(fields)
}

func extract(path string, page int) ([]annotation.Field, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	ctx, err := api.ReadValidateAndOptimize(f, model.NewDefaultConfiguration())
	if err != nil {
		return nil, err
	}
	d, _, _, err := ctx.PageDict(page+1, false)
	if err != nil {
		return nil, err
	}
	annots, err := ctx.DereferenceArray(d["Annots"])
	if err != nil {
		return nil, err
	}
	var fields []annotation.Field
	for _, o := range annots {
		w, err := ctx.DereferenceDict(o)
		if err != nil || w.NameEntry("Subtype") == nil || *w.NameEntry("Subtype") != "Widget" {
			continue
		}
		parent, _ := ctx.DereferenceDict(w["Parent"])
		name, typ := entry(ctx, w, parent, "T"), entry(ctx, w, parent, "FT")
		rect, err := ctx.RectForArray(w.ArrayEntry("Rect"))
		if err != nil || rect == nil {
			continue
		}
		fd := annotation.Field{
			ID:   name,
			Rect: annotation.Rect{X: round(rect.LL.X), Y: round(rect.LL.Y), Width: round(rect.Width()), Height: round(rect.Height())},
			Meta: map[string]any{"acroField": name},
		}
		if typ == "Btn" {
			fd.Kind = annotation.KindCheckbox
		}
		fields = append(fields, fd)
	}
	sort.SliceStable(fields, func(i, j int) bool { // top-to-bottom, then left-to-right
		a, b := fields[i].Rect, fields[j].Rect
		if math.Abs(a.Y+a.Height-(b.Y+b.Height)) > 1 {
			return a.Y+a.Height > b.Y+b.Height
		}
		return a.X < b.X
	})
	return fields, nil
}

// entry reads a field attribute from the widget, falling back to its parent
// (terminal fields often split name and widget into two objects).
func entry(ctx *model.Context, w, parent types.Dict, key string) string {
	for _, d := range []types.Dict{w, parent} {
		if d == nil {
			continue
		}
		o, _ := ctx.Dereference(d[key])
		switch v := o.(type) {
		case types.StringLiteral:
			s, _ := types.StringLiteralToString(v)
			return s
		case types.HexLiteral:
			s, _ := types.HexLiteralToString(v)
			return s
		case types.Name:
			return string(v)
		}
	}
	return ""
}

func round(f float64) float64 { return math.Round(f*10) / 10 }

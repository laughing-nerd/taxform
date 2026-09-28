package annotation

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func ptr[T any](v T) *T { return &v }

func TestFormatValue(t *testing.T) {
	tests := []struct {
		v    any
		f    Format
		copy string
		want string
	}{
		{json.Number("1234.5"), Format{Type: FormatCurrency}, "", "1,234.50"},
		{"98250.505", Format{Type: FormatCurrency}, "", "98,250.51"},     // half away from zero
		{json.Number("0.125"), Format{Type: FormatCurrency}, "", "0.13"}, // binary float would give 0.12
		{json.Number("-1234.5"), Format{Type: FormatCurrency, Negative: "parens"}, "", "(1,234.50)"},
		{json.Number("-0.001"), Format{Type: FormatCurrency}, "", "0.00"}, // never "-0.00"
		{json.Number("0"), Format{Type: FormatCurrency, Zero: "blank"}, "", ""},
		{json.Number("1234567.49"), Format{Type: FormatNumber}, "", "1,234,567"},
		{json.Number("12.3456"), Format{Type: FormatNumber, Decimals: ptr(3)}, "", "12.346"},
		{json.Number("-1234.567"), Format{Type: FormatCurrency, Part: "integer"}, "", "-1,234"},
		{json.Number("1234.567"), Format{Type: FormatCurrency, Part: "fraction"}, "", "57"},
		{"123456789", Format{Type: FormatSSN}, "", "123-45-6789"},
		{"123-45-6789", Format{Type: FormatSSN, Mask: &Mask{Visible: 4, Copies: []string{"B"}}}, "B", "XXX-XX-6789"},
		{"123-45-6789", Format{Type: FormatSSN, Mask: &Mask{Visible: 4, Copies: []string{"B"}}}, "A", "123-45-6789"},
		{"987654321", Format{Type: FormatEIN}, "", "98-7654321"},
		{"2026-04-15", Format{Type: FormatDate}, "", "04/15/2026"},
		{"2026-04-15", Format{Type: FormatDate, Pattern: "YY.MM.DD"}, "", "26.04.15"},
		{"dd", Format{Type: FormatText, Upper: true}, "", "DD"},
		{nil, Format{Type: FormatCurrency}, "", ""},
	}
	for _, tt := range tests {
		got, err := FormatValue(tt.v, tt.f, tt.copy)
		if err != nil || got != tt.want {
			t.Errorf("FormatValue(%v, %+v) = %q, %v; want %q", tt.v, tt.f, got, err, tt.want)
		}
	}
	for _, bad := range []struct {
		v any
		f Format
	}{
		{"12-345", Format{Type: FormatSSN}},
		{"abc", Format{Type: FormatCurrency}},
		{"1/3", Format{Type: FormatCurrency}},
		{"04/15/2026", Format{Type: FormatDate}},
	} {
		if _, err := FormatValue(bad.v, bad.f, ""); err == nil {
			t.Errorf("FormatValue(%v, %s) should fail", bad.v, bad.f.Type)
		}
	}
}

func miniAnnotation() *Annotation {
	return &Annotation{
		SpecVersion: "1.0.0", AnnotationVersion: 1,
		Form:      Form{ID: "t", TaxYear: 2026, Revision: "2026", PDF: PDFRef{SHA256: strings.Repeat("0", 64), PageCount: 1}},
		Instances: "$.items[*]",
		Layout:    Layout{PageSize: Size{612, 792}, Copies: []Copy{{Name: "A", Pages: []int{0}}}},
	}
}

func decode(t *testing.T, s string) any {
	t.Helper()
	v, err := DecodeData(strings.NewReader(s))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func texts(p Page) map[string]string {
	m := map[string]string{}
	for _, tx := range p.Texts {
		m[tx.Field] += tx.Text + "|"
	}
	return m
}

func TestResolvePathsTemplatesGroups(t *testing.T) {
	a := miniAnnotation()
	a.Fields = []Field{
		{ID: "abs", Rect: Rect{10, 700, 200, 12}, Source: "$.owner"},
		{ID: "rel", Rect: Rect{10, 680, 200, 12}, Source: "@.name"},
		{ID: "addr", Kind: KindMultiline, Rect: Rect{10, 600, 200, 60}, Template: "{@.l1}\n{@.l2}\n{@.city}, {@.st}"},
		{ID: "chk", Kind: KindCheckbox, Rect: Rect{300, 700, 10, 10}, Source: "@.status", CheckedValue: "single"},
		{ID: "need", Rect: Rect{300, 680, 100, 12}, Source: "@.missing", Required: true},
		{ID: "many", Rect: Rect{300, 660, 100, 12}, Source: "@.rows[*].v"},
		{ID: "g", Group: &Group{Items: "@.rows[?@.keep == true]", Rows: []Offset{{0, 0}, {0, -20}},
			Fields: []Field{{ID: "v", Rect: Rect{300, 600, 100, 12}, Source: "@.v"}}}},
	}
	data := decode(t, `{"owner":"Pat","items":[{"name":"one","l1":"1 Main","l2":null,"city":"X","st":"CA","status":"single",
		"rows":[{"v":"a","keep":true},{"v":"b","keep":false},{"v":"c","keep":true},{"v":"d","keep":true}]}]}`)

	pages, diags := Resolve(a, data)
	if len(pages) != 1 {
		t.Fatalf("got %d pages", len(pages))
	}
	got := texts(pages[0])
	want := map[string]string{"abs": "Pat|", "rel": "one|", "addr": "1 Main|X, CA|", "chk": "X|", "g[0].v": "a|", "g[1].v": "c|"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	codes := map[string]bool{}
	for _, d := range diags {
		codes[d.Code] = true
	}
	if !codes["missing_required"] || !codes["group_overflow"] || !codes["invalid_path"] {
		t.Errorf("want missing_required, group_overflow and invalid_path, got %v", diags)
	}
}

func TestOverflow(t *testing.T) {
	a := miniAnnotation()
	a.Fields = []Field{
		{ID: "shrink", Rect: Rect{10, 700, 40, 12}, Source: "@.s"},
		{ID: "err", Rect: Rect{10, 660, 40, 12}, Source: "@.s", Overflow: OverflowError},
		{ID: "comb", Kind: KindComb, Cells: 4, Rect: Rect{10, 640, 40, 12}, Source: "@.s"},
	}
	pages, _ := Resolve(a, decode(t, `{"items":[{"s":"ABCDEFG"}]}`))
	var shrink Text
	for _, tx := range pages[0].Texts {
		if tx.Field == "shrink" {
			shrink = tx
		}
	}
	// 36pt available / (0.6 * 7 chars) = 8.57pt, so shrink stops at 8.5.
	if shrink.Size != 8.5 || shrink.Text != "ABCDEFG" {
		t.Errorf("shrink = %+v", shrink)
	}
	got := texts(pages[0])
	if got["err"] != "" || got["comb"] != "A|B|C|D|" {
		t.Errorf("overflow results: %v", got)
	}
}

func TestExampleW2(t *testing.T) {
	f, err := os.Open("../examples/w2-2026.annotation.json")
	if err != nil {
		t.Fatal(err)
	}
	a, err := Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	pdf, err := os.ReadFile("../examples/fw2.pdf")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range Validate(a, pdf) {
		t.Errorf("validate: %s", d)
	}
	df, _ := os.Open("../examples/taxpayer.json")
	data, err := DecodeData(df)
	if err != nil {
		t.Fatal(err)
	}
	pages, diags := Resolve(a, data)
	for _, d := range diags {
		if d.Severity == "error" {
			t.Errorf("resolve: %s", d)
		}
	}
	if len(pages) != 12 { // 6 copies x 2 sheets (3 W-2s, 2 per sheet)
		t.Fatalf("got %d pages, want 12", len(pages))
	}
	copyB := texts(pages[4]) // copy order A,1,B: B sheet 0 is index 4
	copyA := texts(pages[0])
	if copyB["a"] != "XXX-XX-6789|XXX-XX-6789|" || copyA["a"] != "123-45-6789|123-45-6789|" {
		t.Errorf("ssn masking: A=%q B=%q", copyA["a"], copyB["a"])
	}
	if copyA["box1"] != "98,250.50|18,640.00|" {
		t.Errorf("box1 across slots = %q", copyA["box1"])
	}
}

func TestValidateGroupRules(t *testing.T) {
	a := miniAnnotation()
	a.Fields = []Field{
		{ID: "outer", Source: "@.v", Group: &Group{Items: "@.rows", Rows: []Offset{{0, 0}}, Fields: []Field{
			{ID: "inner", Group: &Group{Items: "@.x", Rows: []Offset{{0, 0}}}},
		}}},
	}
	msgs := ""
	for _, d := range Validate(a, nil) {
		msgs += d.Message + "\n"
	}
	if !strings.Contains(msgs, "groups cannot be nested") || !strings.Contains(msgs, "only takes id, label, page, copies and group") {
		t.Errorf("want nesting and group-only errors, got:\n%s", msgs)
	}
}

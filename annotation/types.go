// Package annotation defines the Tax Form Annotation (TFA) format: a
// declarative description of where every box on a tax form is, where its
// value comes from in a data set, and how that value is printed.
//
// An annotation file is written once per form revision (for example the
// 2026 Form W-2) and reused for every taxpayer. A renderer combines three
// inputs: the blank PDF, the annotation, and the taxpayer data.
//
// Conventions used throughout:
//   - Units are PDF points (1/72 inch).
//   - The origin is the bottom-left corner of the page (PDF user space)
//   - Page indexes are 0-based.
//   - Paths are RFC 9535 JSONPath. Ref: https://www.rfc-editor.org/info/rfc9535/
package annotation

// Annotation describes one revision of one form.
type Annotation struct {
	// Version of the TFA format this file is written against (semver).
	SpecVersion string `json:"specVersion" jsonschema:"pattern=^[0-9]+\\.[0-9]+\\.[0-9]+$"`
	// Revision of this annotation file. Published files are immutable: any
	// correction produces a new file with annotationVersion + 1.
	AnnotationVersion int `json:"annotationVersion" jsonschema:"minimum=1"`
	// The exact blank form this annotation describes.
	Form Form `json:"form"`
	// JSONPath selecting the form instances to render: one form is printed
	// per match (e.g. one W-2 per employer), and "@" in field paths refers to
	// that match. Omit for forms printed once per return; "@" is then the
	// data root.
	Instances string `json:"instances,omitempty"`
	// Page geometry, copies, and instance slots.
	Layout Layout `json:"layout"`
	// Every box on the form, in reading order. Repeating rows (W-2 box 12,
	// state rows 15-20) are fields with a group.
	Fields []Field `json:"fields"`
}

// Form identifies the blank form. Together, ID + TaxYear + Revision select an
// annotation; PDF.SHA256 guarantees coordinates are applied to the right file.
type Form struct {
	// Stable identifier, e.g. "irs-w2", "irs-1040", "ca-540".
	ID string `json:"id" jsonschema:"pattern=^[a-z0-9-]+$"`
	// Human-readable title as printed on the form.
	Title string `json:"title"`
	// Issuing authority: "US" for federal, "US-CA" for California, etc.
	Jurisdiction string `json:"jurisdiction"`
	// Tax year the form is used for.
	TaxYear int `json:"taxYear" jsonschema:"minimum=1900"`
	// Revision as printed on the form, e.g. "2026" or "Rev. December 2025".
	Revision string `json:"revision"`
	// The blank PDF this annotation was authored against.
	PDF PDFRef `json:"pdf"`
}

// PDFRef pins the blank PDF.
type PDFRef struct {
	// Lowercase hex SHA-256 of the blank PDF. Renderers must refuse to render
	// onto a file with a different hash.
	SHA256 string `json:"sha256" jsonschema:"pattern=^[0-9a-f]{64}$"`
	// Number of pages in the blank PDF.
	PageCount int `json:"pageCount" jsonschema:"minimum=1"`
	// Where the PDF was obtained (informational).
	Source string `json:"source,omitempty"`
}

// Layout describes the physical structure of the form.
type Layout struct {
	// Size of every page in points
	PageSize Size `json:"pageSize"`
	// Copies of the form contained in the PDF (W-2 has A, 1, B, C, 2, D).
	// A single-copy form has one entry.
	Copies []Copy `json:"copies" jsonschema:"minItems=1"`
	// Positions of form instances on one sheet. The W-2 prints two instances
	// per page (top and bottom), so it has two slots. Field rectangles are
	// authored for the first slot and shifted by each slot's offset.
	// Defaults to a single slot at offset (0, 0).
	Slots []Offset `json:"slots,omitempty"`
}

// Size is a width and height in points.
type Size struct {
	Width  float64 `json:"width" jsonschema:"exclusiveMinimum=0"`
	Height float64 `json:"height" jsonschema:"exclusiveMinimum=0"`
}

// Copy is one copy of the form (e.g. "Copy B - To Be Filed With Employee's
// Federal Tax Return") and the PDF pages it occupies.
type Copy struct {
	// Short name, e.g. "A", "B", "2". Referenced by Field.Copies and Mask.Copies.
	Name string `json:"name"`
	// What the copy is for.
	Description string `json:"description,omitempty"`
	// 0-based PDF page indexes, in order. Field.Page indexes into this list.
	Pages []int `json:"pages" jsonschema:"minItems=1"`
}

// Offset is a translation in points.
type Offset struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// Rect is a box in points, measured from the bottom-left of the page.
type Rect struct {
	X      float64 `json:"x" jsonschema:"minimum=0"`
	Y      float64 `json:"y" jsonschema:"minimum=0"`
	Width  float64 `json:"width" jsonschema:"exclusiveMinimum=0"`
	Height float64 `json:"height" jsonschema:"exclusiveMinimum=0"`
}

// Field is one box on the form.
type Field struct {
	// Unique within the annotation (within its group, for a group's fields).
	ID string `json:"id" jsonschema:"pattern=^[A-Za-z0-9_.-]+$"`
	// The box label as printed, e.g. "1 Wages, tips, other compensation".
	Label string `json:"label,omitempty"`
	// Layout kind. Defaults to "text".
	Kind string `json:"kind,omitempty" jsonschema:"enum=text,enum=multiline,enum=comb,enum=checkbox"`
	// Index into the copy's Pages list. Defaults to 0.
	Page int `json:"page,omitempty" jsonschema:"minimum=0"`
	// Restrict this field to these copies (e.g. VOID only exists on A and D).
	// Empty means every copy.
	Copies []string `json:"copies,omitempty"`
	// Where the value is printed, for the first slot and first group row.
	// Required, except on a field with a group.
	Rect Rect `json:"rect,omitempty"`
	// JSONPath to the value. Exactly one of Source or Template is required
	// (checkboxes require Source).
	Source string `json:"source,omitempty"`
	// Text with embedded paths, e.g. "{@.city}, {@.state} {@.zip}". Lines
	// whose placeholders are all empty are dropped. Output is plain text.
	Template string `json:"template,omitempty"`
	// Checkbox only: the value that checks the box. When omitted the box is
	// checked by boolean true. Use it for radio-like groups, e.g. filing
	// status "single" / "mfj", one checkbox field per option.
	CheckedValue any `json:"checkedValue,omitempty"`
	// Comb only: number of character cells.
	Cells int `json:"cells,omitempty" jsonschema:"minimum=1"`
	// How the value is converted to text. Defaults to text.
	Format *Format `json:"format,omitempty"`
	// Typography; unset keys take the built-in defaults (see Style).
	Style *Style `json:"style,omitempty"`
	// What to do when the text does not fit: "shrink" (default) makes the
	// font smaller down to minSize, then cuts the text and warns; "error"
	// prints nothing and reports an error.
	Overflow string `json:"overflow,omitempty" jsonschema:"enum=shrink,enum=error"`
	// Report an error when the value is missing or null.
	Required bool `json:"required,omitempty"`
	// Free-form extension data (e.g. e-file XPath, extraction hints).
	Meta map[string]any `json:"meta,omitempty"`
	// Repeating rows. When set, this field is a group: it takes only id,
	// label, page, copies and group, and prints its inner fields once per row.
	Group *Group `json:"group,omitempty"`
}

// Group repeats a set of fields once per item of a list: W-2 box 12 (four
// code/amount rows), the two state rows, or a dependents table.
type Group struct {
	// JSONPath to the list whose items fill the rows, one item per row. If it
	// selects a single array, the array's elements are the items. Filters
	// work: "@.codes[?@.code != 'DD']". Inside the group "@" is the item.
	Items string `json:"items"`
	// One offset per printed row, applied to every inner field's rectangle.
	// More items than rows is reported as an error and only the first
	// len(rows) items are printed.
	Rows []Offset `json:"rows" jsonschema:"minItems=1"`
	// The fields of one row, with rectangles for the first row. They inherit
	// page and copies from the group's field. Groups cannot be nested.
	Fields []Field `json:"fields" jsonschema:"minItems=1"`
}

// Format converts a data value to the printed string. Options that do not
// apply to Type are invalid.
type Format struct {
	Type string `json:"type" jsonschema:"enum=text,enum=currency,enum=number,enum=ssn,enum=ein,enum=date"`
	// currency, number: digits after the decimal point. Rounding is half away
	// from zero on the exact decimal value (never binary floating point).
	// Defaults to 2 for currency and 0 for number.
	Decimals *int `json:"decimals,omitempty" jsonschema:"minimum=0,maximum=6"`
	// currency, number: "minus" (-1,234.00, default) or "parens" ((1,234.00)).
	Negative string `json:"negative,omitempty" jsonschema:"enum=minus,enum=parens"`
	// currency, number: "show" (default) prints zero; "blank" prints nothing.
	Zero string `json:"zero,omitempty" jsonschema:"enum=show,enum=blank"`
	// currency, number: print only one part of the rounded amount, for forms
	// with separate dollars and cents boxes. "integer" prints 1,234 (with
	// sign), "fraction" prints the decimal digits only (50). Default: both.
	Part string `json:"part,omitempty" jsonschema:"enum=integer,enum=fraction"`
	// ssn, ein: replace leading digits, e.g. XXX-XX-6789 on employee copies.
	Mask *Mask `json:"mask,omitempty"`
	// date: output pattern using YYYY, YY, MM, DD. Defaults to "MM/DD/YYYY".
	Pattern string `json:"pattern,omitempty"`
	// text: print in capital letters.
	Upper bool `json:"upper,omitempty"`
}

// Mask truncates an identifier, keeping only the last Visible digits.
type Mask struct {
	// Number of trailing digits left visible (IRS truncation keeps 4).
	Visible int `json:"visible" jsonschema:"minimum=0,maximum=9"`
	// Replacement character. Defaults to "X".
	Char string `json:"char,omitempty" jsonschema:"minLength=1,maxLength=1"`
	// Apply only on these copies (e.g. employee copies B, C, 2). Empty means
	// every copy.
	Copies []string `json:"copies,omitempty"`
}

// Style controls size and placement inside the rectangle. Unset keys take
// the built-in default given on each key. The font is always Courier.
type Style struct {
	// Font size in points. Defaults to 10.
	Size float64 `json:"size,omitempty" jsonschema:"minimum=0"`
	// Smallest size "shrink" may use. Defaults to 6 (or Size, if smaller).
	MinSize float64 `json:"minSize,omitempty" jsonschema:"minimum=0"`
	// "left", "center" or "right". Defaults to right for currency and number,
	// left otherwise.
	Align string `json:"align,omitempty" jsonschema:"enum=left,enum=center,enum=right"`
	// "top" or "middle". Defaults to top for multiline, middle otherwise.
	VAlign string `json:"valign,omitempty" jsonschema:"enum=top,enum=middle"`
	// Inner spacing on every side, in points. Defaults to 2.
	Padding *float64 `json:"padding,omitempty" jsonschema:"minimum=0"`
}

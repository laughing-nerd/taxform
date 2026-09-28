package annotation

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/theory/jsonpath"
)

// Page is one output page: which blank PDF page to use and what to draw on it.
type Page struct {
	Source int    `json:"source"` // 0-based page index in the blank PDF
	Copy   string `json:"copy"`
	Sheet  int    `json:"sheet"` // 0-based; >0 when there are more instances than slots
	Boxes  []Box  `json:"boxes"` // every placed rectangle, filled or not (for debug outlines)
	Texts  []Text `json:"texts"`
}

// Box is a field's rectangle as placed on the page.
type Box struct {
	Field string `json:"field"`
	Rect  Rect   `json:"rect"`
}

// Text is one piece of text to draw in Courier, its baseline starting at (X, Y).
type Text struct {
	Field string  `json:"field"`
	Text  string  `json:"text"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	Size  float64 `json:"size"`
}

// Diagnostic reports a problem found while validating or resolving.
type Diagnostic struct {
	Severity string `json:"severity"` // SeverityError or SeverityWarning
	Code     string `json:"code"`
	Instance int    `json:"instance"` // form instance index, -1 if not applicable
	Field    string `json:"field,omitempty"`
	Message  string `json:"message"`
}

func (d Diagnostic) String() string {
	where := d.Field
	if d.Instance >= 0 {
		where = fmt.Sprintf("instance %d %s", d.Instance, d.Field)
	}
	return fmt.Sprintf("%-7s %-18s %s: %s", d.Severity, d.Code, strings.TrimSpace(where), d.Message)
}

// Decode reads an annotation strictly: unknown keys are errors, which
// catches typos such as "sorce".
func Decode(r io.Reader) (*Annotation, error) {
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	var a Annotation
	if err := decoder.Decode(&a); err != nil {
		return nil, err
	}
	return &a, nil
}

// DecodeData reads a taxpayer data set, keeping numbers exact (as
// json.Number) instead of converting them to floating point.
func DecodeData(r io.Reader) (any, error) {
	decoder := json.NewDecoder(r)
	decoder.UseNumber()
	var data any
	err := decoder.Decode(&data)
	return data, err
}

// Resolve works out every page and every piece of text for one data set,
// without drawing anything. It never fails as a whole: problems are
// reported as diagnostics and the affected box is left blank, so one bad
// value cannot hide the rest of the form.
func Resolve(a *Annotation, data any) ([]Page, []Diagnostic) {
	r := newResolver(a, data)

	// One form per instance: one per W-2, or just one for the whole return.
	instances := []any{data}
	if a.Instances != "" {
		matches, err := r.selectPath(a.Instances, data)
		if err != nil {
			r.report(SeverityError, "invalid_path", "instances", err.Error())
			return nil, r.diagnostics
		}
		instances = matches
		if len(matches) == 0 {
			r.report(SeverityWarning, "no_instances", "instances", "instances matched nothing; no pages rendered")
		}
	}

	slots := slotsOf(a)
	sheetsNeeded := (len(instances) + len(slots) - 1) / len(slots)
	var pages []Page
	for _, formCopy := range a.Layout.Copies {
		for sheet := 0; sheet < sheetsNeeded; sheet++ {
			for pageIndex, pdfPage := range formCopy.Pages {
				page := Page{Source: pdfPage, Copy: formCopy.Name, Sheet: sheet, Boxes: []Box{}, Texts: []Text{}}
				for slotIndex, slotOffset := range slots {
					instanceIndex := sheet*len(slots) + slotIndex
					if instanceIndex >= len(instances) {
						break // last sheet: leave the remaining slots blank
					}
					r.instance = instanceIndex
					r.placeInstance(&page, instances[instanceIndex], slotOffset, formCopy.Name, pageIndex)
				}
				pages = append(pages, page)
			}
		}
	}
	return pages, dedupe(r.diagnostics)
}

// slotsOf returns the instance positions on a sheet: one at (0, 0) unless
// the layout lists more (the W-2 has a top and a bottom slot).
func slotsOf(a *Annotation) []Offset {
	if len(a.Layout.Slots) == 0 {
		return []Offset{{}}
	}
	return a.Layout.Slots
}

type resolver struct {
	a           *Annotation
	root        any                       // the whole data set, for "$" paths
	paths       map[string]*jsonpath.Path // parsed paths, so each is parsed once
	diagnostics []Diagnostic
	instance    int // which form instance is being placed, for diagnostics
}

func newResolver(a *Annotation, root any) *resolver {
	return &resolver{a: a, root: root, paths: map[string]*jsonpath.Path{}}
}

func (r *resolver) report(severity, code, field, message string) {
	r.diagnostics = append(r.diagnostics, Diagnostic{severity, code, r.instance, field, message})
}

// placeInstance places one form instance's fields on one page, in order.
// item is what "@" refers to: the instance's data (e.g. one W-2).
func (r *resolver) placeInstance(page *Page, item any, offset Offset, copyName string, pageIndex int) {
	for _, field := range r.a.Fields {
		if field.Page != pageIndex || !appliesTo(field.Copies, copyName) {
			continue
		}
		if field.Group != nil {
			r.placeGroup(page, field, item, offset, copyName)
		} else {
			r.placeField(page, field, field.ID, item, offset, copyName)
		}
	}
}

// placeGroup prints a group's fields once per row, filling row 1 from the
// first item of the list, row 2 from the second, and so on.
func (r *resolver) placeGroup(page *Page, field Field, item any, offset Offset, copyName string) {
	group := field.Group
	items, err := r.selectItems(group.Items, item)
	if err != nil {
		r.report(SeverityError, "invalid_path", field.ID, err.Error())
		return
	}
	if len(items) > len(group.Rows) {
		r.report(SeverityError, "group_overflow", field.ID,
			fmt.Sprintf("%d items but only %d rows; printed the first %d", len(items), len(group.Rows), len(group.Rows)))
	}
	for rowIndex, rowOffset := range group.Rows {
		rowPosition := Offset{offset.X + rowOffset.X, offset.Y + rowOffset.Y}
		for _, inner := range group.Fields {
			id := fmt.Sprintf("%s[%d].%s", field.ID, rowIndex, inner.ID)
			if rowIndex < len(items) {
				r.placeField(page, inner, id, items[rowIndex], rowPosition, copyName)
			} else {
				page.Boxes = append(page.Boxes, Box{id, shift(inner.Rect, rowPosition)}) // empty row
			}
		}
	}
}

// appliesTo reports whether a field restricted to copies appears on copyName.
func appliesTo(copies []string, copyName string) bool {
	return len(copies) == 0 || slices.Contains(copies, copyName)
}

func shift(rect Rect, by Offset) Rect {
	rect.X += by.X
	rect.Y += by.Y
	return rect
}

// placeField looks up one box's value, formats it and places the text.
func (r *resolver) placeField(page *Page, field Field, id string, item any, offset Offset, copyName string) {
	rect := shift(field.Rect, offset)
	page.Boxes = append(page.Boxes, Box{id, rect})
	format := formatOf(field)
	style := EffectiveStyle(field, format)

	if field.Kind == KindCheckbox {
		r.placeCheckbox(page, field, id, item, rect, style)
		return
	}

	lines, err := r.textLines(field, item, format, copyName)
	if err != nil {
		code := "format_error"
		if errors.Is(err, errBadPath) {
			code = "invalid_path"
		}
		r.report(SeverityError, code, id, err.Error())
		return
	}
	if len(lines) == 0 {
		if field.Required {
			r.report(SeverityError, "missing_required", id, "required value is missing")
		}
		return
	}
	for i := range lines {
		lines[i] = r.replaceUnprintable(id, lines[i])
	}

	switch field.Kind {
	case KindComb:
		r.placeComb(page, field, id, rect, style, lines[0])
	case KindMultiline:
		r.placeMultiline(page, field, id, rect, style, lines)
	default:
		r.placeLine(page, field, id, rect, style, lines[0])
	}
}

// placeCheckbox prints the mark, centered, when the value checks the box.
func (r *resolver) placeCheckbox(page *Page, field Field, id string, item any, rect Rect, style Style) {
	value, err := r.value(field.Source, item)
	if err != nil {
		r.report(SeverityError, "invalid_path", id, err.Error())
		return
	}
	if value == nil && field.Required {
		r.report(SeverityError, "missing_required", id, "required value is missing")
	}
	if !isChecked(value, field.CheckedValue) {
		return
	}
	x := rect.X + (rect.Width-textWidth(checkboxMark, style.Size))/2
	page.Texts = append(page.Texts, Text{id, checkboxMark, x, firstBaseline(rect, style, 1), style.Size})
}

// isChecked: without checkedValue the box is checked by JSON true; with it,
// by a value equal to checkedValue.
func isChecked(value, checkedValue any) bool {
	if checkedValue == nil {
		isTrue, ok := value.(bool)
		return ok && isTrue
	}
	got, err1 := toText(value)
	want, err2 := toText(checkedValue)
	return err1 == nil && err2 == nil && got == want
}

// textLines returns the formatted text for a field: one entry for a single
// line, one per line for multiline. Empty means there is nothing to print.
func (r *resolver) textLines(field Field, item any, format Format, copyName string) ([]string, error) {
	multiline := field.Kind == KindMultiline
	if field.Template != "" {
		return r.fillTemplate(field.Template, item, format, copyName, multiline)
	}

	var values []any
	if multiline {
		// Several matches are allowed: each one (or each element of a
		// matched list) becomes a line.
		matches, err := r.selectPath(field.Source, item)
		if err != nil {
			return nil, err
		}
		for _, match := range matches {
			if list, isList := match.([]any); isList {
				values = append(values, list...)
			} else {
				values = append(values, match)
			}
		}
	} else {
		value, err := r.value(field.Source, item)
		if err != nil {
			return nil, err
		}
		values = []any{value}
	}

	var lines []string
	for _, value := range values {
		text, err := FormatValue(value, format, copyName)
		if err != nil {
			return nil, err
		}
		if text == "" {
			continue
		}
		for _, line := range strings.Split(text, "\n") {
			if line != "" || multiline { // multiline keeps blank lines inside a value
				lines = append(lines, line)
			}
		}
	}
	return lines, nil
}

// fillTemplate replaces each {path} in a template with its value, line by
// line. A line whose placeholders are all empty, such as a missing
// "address line 2", is dropped. A single-line field uses the first line
// that is kept.
func (r *resolver) fillTemplate(template string, item any, format Format, copyName string, multiline bool) ([]string, error) {
	var lines []string
	for _, line := range strings.Split(template, "\n") {
		paths := templatePaths(line)
		anyValue := false
		for _, path := range paths {
			value, err := r.value(path, item)
			if err != nil {
				return nil, err
			}
			text, err := FormatValue(value, Format{Type: FormatText, Upper: format.Upper}, copyName)
			if err != nil {
				return nil, err
			}
			if text != "" {
				anyValue = true
			}
			line = strings.Replace(line, "{"+path+"}", text, 1)
		}
		if len(paths) > 0 && !anyValue {
			continue
		}
		lines = append(lines, strings.TrimSpace(line))
		if !multiline {
			break
		}
	}
	return lines, nil
}

// templatePaths returns the paths written inside {braces} in a template.
func templatePaths(template string) []string {
	var paths []string
	rest := template
	for {
		start := strings.Index(rest, "{")
		if start < 0 {
			return paths
		}
		length := strings.Index(rest[start:], "}")
		if length < 0 {
			return paths
		}
		paths = append(paths, rest[start+1:start+length])
		rest = rest[start+length+1:]
	}
}

// replaceUnprintable swaps characters the standard PDF fonts cannot print
// for "?". They can print Latin-1: English letters, digits, punctuation and
// accented letters such as é and ñ.
func (r *resolver) replaceUnprintable(id, text string) string {
	var result strings.Builder
	replaced := false
	for _, char := range text {
		printable := char >= 0x20 && char <= 0x7E || char >= 0xA0 && char <= 0xFF
		if printable {
			result.WriteRune(char)
		} else {
			result.WriteRune('?')
			replaced = true
		}
	}
	if replaced {
		r.report(SeverityWarning, "unsupported_char", id, fmt.Sprintf("%q has characters outside Latin-1; replaced with '?'", text))
	}
	return result.String()
}

// textWidth is the printed width of text in Courier at a given size.
func textWidth(text string, size float64) float64 {
	return float64(utf8.RuneCountInString(text)) * charWidth * size
}

// placeLine prints one line, shrinking or cutting it if it is too wide.
func (r *resolver) placeLine(page *Page, field Field, id string, rect Rect, style Style, text string) {
	padding := *style.Padding
	availableWidth := rect.Width - 2*padding

	size := style.Size
	if field.Overflow != OverflowError {
		for textWidth(text, size) > availableWidth+tolerance && size-shrinkStep >= style.MinSize-tolerance {
			size -= shrinkStep
		}
	}
	if textWidth(text, size) > availableWidth+tolerance {
		if field.Overflow == OverflowError {
			r.report(SeverityError, "overflow", id, fmt.Sprintf("%q does not fit; left blank", text))
			return
		}
		keep := int(math.Floor(availableWidth/(charWidth*size) + tolerance))
		r.report(SeverityWarning, "overflow", id, fmt.Sprintf("%q truncated to %d characters", text, keep))
		text = string([]rune(text)[:keep])
	}

	style.Size = size
	x := alignedX(rect, style, textWidth(text, size))
	page.Texts = append(page.Texts, Text{id, text, x, firstBaseline(rect, style, 1), size})
}

// placeMultiline wraps paragraphs into lines, shrinking or cutting them if
// there are too many lines for the box.
func (r *resolver) placeMultiline(page *Page, field Field, id string, rect Rect, style Style, paragraphs []string) {
	padding := *style.Padding
	size := style.Size
	lines, maxLines := wrapForBox(paragraphs, rect, padding, size)
	if field.Overflow != OverflowError {
		for len(lines) > maxLines && size-shrinkStep >= style.MinSize-tolerance {
			size -= shrinkStep
			lines, maxLines = wrapForBox(paragraphs, rect, padding, size)
		}
	}
	if len(lines) > maxLines {
		if field.Overflow == OverflowError {
			r.report(SeverityError, "overflow", id, fmt.Sprintf("%d lines do not fit; left blank", len(lines)))
			return
		}
		r.report(SeverityWarning, "overflow", id, fmt.Sprintf("%d lines truncated to %d", len(lines), maxLines))
		lines = lines[:max(maxLines, 0)]
	}

	style.Size = size
	y := firstBaseline(rect, style, len(lines))
	for _, line := range lines {
		x := alignedX(rect, style, textWidth(line, size))
		page.Texts = append(page.Texts, Text{id, line, x, y, size})
		y -= lineHeight * size
	}
}

// wrapForBox wraps paragraphs to the box width at a font size and returns
// the lines plus how many lines fit in the box height.
func wrapForBox(paragraphs []string, rect Rect, padding, size float64) (lines []string, maxLines int) {
	charsPerLine := int(math.Floor((rect.Width-2*padding)/(charWidth*size) + tolerance))
	heightForMoreLines := rect.Height - 2*padding - capHeight*size
	maxLines = 1 + int(math.Floor(heightForMoreLines/(lineHeight*size)+tolerance))
	return wrap(paragraphs, charsPerLine), maxLines
}

// placeComb prints one character per equal-width cell, like the digit
// boxes of a bank routing number.
func (r *resolver) placeComb(page *Page, field Field, id string, rect Rect, style Style, text string) {
	chars := []rune(text)
	if len(chars) > field.Cells {
		if field.Overflow == OverflowError {
			r.report(SeverityError, "overflow", id, fmt.Sprintf("%q has more than %d characters; left blank", text, field.Cells))
			return
		}
		r.report(SeverityWarning, "overflow", id, fmt.Sprintf("%q truncated to %d characters", text, field.Cells))
		chars = chars[:field.Cells]
	}
	cellWidth := rect.Width / float64(field.Cells)
	firstCell := 0
	if style.Align == AlignRight {
		firstCell = field.Cells - len(chars)
	}
	y := firstBaseline(rect, style, 1)
	for i, char := range chars {
		cellLeft := rect.X + float64(firstCell+i)*cellWidth
		x := cellLeft + (cellWidth-charWidth*style.Size)/2 // centered in its cell
		page.Texts = append(page.Texts, Text{id, string(char), x, y, style.Size})
	}
}

// alignedX returns where text of the given width starts inside the box.
func alignedX(rect Rect, style Style, width float64) float64 {
	padding := *style.Padding
	switch style.Align {
	case AlignRight:
		return rect.X + rect.Width - padding - width
	case AlignCenter:
		return rect.X + (rect.Width-width)/2
	}
	return rect.X + padding
}

// firstBaseline returns the y of the first line's baseline (the line letters
// sit on) for lineCount lines.
func firstBaseline(rect Rect, style Style, lineCount int) float64 {
	capitals := capHeight * style.Size
	if style.VAlign == VAlignTop {
		return rect.Y + rect.Height - *style.Padding - capitals
	}
	// Middle: center the block from the top of the first line's capitals
	// down to the last line's baseline.
	blockHeight := capitals + float64(lineCount-1)*lineHeight*style.Size
	return rect.Y + (rect.Height+blockHeight)/2 - capitals
}

// wrap breaks paragraphs into lines of at most width characters, breaking
// at spaces. A word longer than a whole line is split.
func wrap(paragraphs []string, width int) []string {
	width = max(width, 1)
	var lines []string
	for _, paragraph := range paragraphs {
		line := ""
		for _, word := range strings.Fields(paragraph) {
			for utf8.RuneCountInString(word) > width {
				if line != "" {
					lines, line = append(lines, line), ""
				}
				chars := []rune(word)
				lines, word = append(lines, string(chars[:width])), string(chars[width:])
			}
			switch {
			case line == "":
				line = word
			case utf8.RuneCountInString(line)+1+utf8.RuneCountInString(word) <= width:
				line += " " + word
			default:
				lines, line = append(lines, line), word
			}
		}
		lines = append(lines, line)
	}
	return lines
}

// formatOf returns the field's format, or plain text if it has none.
func formatOf(field Field) Format {
	if field.Format != nil {
		return *field.Format
	}
	return Format{Type: FormatText}
}

// EffectiveStyle fills every style key the field leaves unset with its
// built-in default, exactly as described in SPEC.md §8.1.
func EffectiveStyle(field Field, format Format) Style {
	var style Style
	if field.Style != nil {
		style = *field.Style
	}
	if style.Size == 0 {
		style.Size = defaultFontSize
	}
	if style.MinSize == 0 {
		style.MinSize = defaultMinFontSize
	}
	style.MinSize = min(style.MinSize, style.Size)
	if style.Align == "" {
		style.Align = AlignLeft
		if format.Type == FormatCurrency || format.Type == FormatNumber {
			style.Align = AlignRight // money lines up on the right
		}
	}
	if style.VAlign == "" {
		style.VAlign = VAlignMiddle
		if field.Kind == KindMultiline {
			style.VAlign = VAlignTop
		}
	}
	if style.Padding == nil {
		padding := defaultPadding
		style.Padding = &padding
	}
	return style
}

// errBadPath marks errors caused by a path rather than by the value found,
// so they are reported as "invalid_path" instead of "format_error".
var errBadPath = errors.New("invalid path")

// compile parses a path once and caches it. A path starting with "@" is
// relative to the current item: it is rewritten to "$" and later run
// against that item instead of the whole data set.
func (r *resolver) compile(path string) (parsed *jsonpath.Path, relative bool, err error) {
	relative = strings.HasPrefix(path, "@")
	if !relative && !strings.HasPrefix(path, "$") {
		return nil, false, fmt.Errorf("%w: %q must start with $ or @", errBadPath, path)
	}
	if cached, ok := r.paths[path]; ok {
		return cached, relative, nil
	}
	query := path
	if relative {
		query = "$" + path[1:]
	}
	parsed, err = jsonpath.Parse(query)
	if err != nil {
		return nil, false, fmt.Errorf("%w: %q: %v", errBadPath, path, err)
	}
	r.paths[path] = parsed
	return parsed, relative, nil
}

// selectPath runs a path and returns every match.
func (r *resolver) selectPath(path string, item any) ([]any, error) {
	parsed, relative, err := r.compile(path)
	if err != nil {
		return nil, err
	}
	if relative {
		return parsed.Select(item), nil
	}
	return parsed.Select(r.root), nil
}

// value runs a path that should match one value. No match (or JSON null)
// returns nil; several matches are an error, because a box shows one value.
func (r *resolver) value(path string, item any) (any, error) {
	matches, err := r.selectPath(path, item)
	if err != nil {
		return nil, err
	}
	switch len(matches) {
	case 0:
		return nil, nil
	case 1:
		return matches[0], nil
	}
	return nil, fmt.Errorf("%w: %q matched %d values; expected one", errBadPath, path, len(matches))
}

// selectItems returns a group's items: the elements of the list the path
// points to, or each match if the path selects several values.
func (r *resolver) selectItems(path string, item any) ([]any, error) {
	matches, err := r.selectPath(path, item)
	if err != nil {
		return nil, err
	}
	if len(matches) == 1 {
		if list, isList := matches[0].([]any); isList {
			return list, nil
		}
		if matches[0] == nil {
			return nil, nil
		}
	}
	return matches, nil
}

// dedupe removes repeated diagnostics (the same problem is found once per
// copy of the form).
func dedupe(diagnostics []Diagnostic) []Diagnostic {
	seen := map[Diagnostic]bool{}
	var unique []Diagnostic
	for _, d := range diagnostics {
		if !seen[d] {
			seen[d] = true
			unique = append(unique, d)
		}
	}
	return unique
}

package annotation

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Validate checks an annotation without any taxpayer data. If pdf is non-nil
// it is also checked against form.pdf (hash and page count). To check paths
// against real data, run Resolve on sample data and read its diagnostics.
func Validate(a *Annotation, pdf []byte) []Diagnostic {
	v := &validator{a: a, paths: newResolver(a, nil)}
	v.checkHeader(pdf)
	v.checkLayout()

	ids := map[string]bool{}
	for _, field := range a.Fields {
		if ids[field.ID] {
			v.fail("duplicate_id", field.ID, "field id is not unique")
		}
		ids[field.ID] = true
		if field.Group != nil {
			v.checkGroup(field)
		} else {
			v.checkField(field, field.ID, field.Page, field.Copies)
		}
	}
	v.checkGeometry()
	return dedupe(v.diagnostics)
}

type validator struct {
	a           *Annotation
	paths       *resolver // only used to parse paths
	diagnostics []Diagnostic
}

func (v *validator) fail(code, field, message string) {
	v.diagnostics = append(v.diagnostics, Diagnostic{SeverityError, code, -1, field, message})
}

func (v *validator) warn(code, field, message string) {
	v.diagnostics = append(v.diagnostics, Diagnostic{SeverityWarning, code, -1, field, message})
}

// oneOf reports an error when value is set but is not one of allowed.
func (v *validator) oneOf(field, key, value string, allowed ...string) {
	if value != "" && !slices.Contains(allowed, value) {
		v.fail("invalid_value", field, fmt.Sprintf("%s %q must be one of: %s", key, value, strings.Join(allowed, ", ")))
	}
}

// checkPath reports a path that cannot be parsed.
func (v *validator) checkPath(field, path string) {
	if _, _, err := v.paths.compile(path); err != nil {
		v.fail("invalid_path", field, err.Error())
	}
}

func (v *validator) checkHeader(pdf []byte) {
	a := v.a
	fileMajor, _, _ := strings.Cut(a.SpecVersion, ".")
	supportedMajor, _, _ := strings.Cut(SpecVersion, ".")
	if fileMajor != supportedMajor {
		v.fail("unsupported_spec", "specVersion", fmt.Sprintf("%q is not supported (this implementation reads %s.x)", a.SpecVersion, supportedMajor))
	}
	if a.AnnotationVersion < 1 {
		v.fail("invalid_header", "annotationVersion", "must be >= 1")
	}
	if a.Form.ID == "" || a.Form.TaxYear == 0 || a.Form.Revision == "" {
		v.fail("invalid_header", "form", "id, taxYear and revision are required")
	}
	if len(a.Form.PDF.SHA256) != 64 {
		v.fail("invalid_header", "form.pdf.sha256", "must be a 64-character hex SHA-256")
	}
	if pdf != nil {
		sum := sha256.Sum256(pdf)
		if actual := hex.EncodeToString(sum[:]); actual != a.Form.PDF.SHA256 {
			v.fail("pdf_mismatch", "form.pdf.sha256", fmt.Sprintf("PDF hash is %s; this annotation was authored for a different file", actual))
		}
	}
	if a.Instances != "" {
		v.checkPath("instances", a.Instances)
	}
}

func (v *validator) checkLayout() {
	layout := v.a.Layout
	if layout.PageSize.Width <= 0 || layout.PageSize.Height <= 0 {
		v.fail("invalid_layout", "layout.pageSize", "width and height must be positive")
	}
	if len(layout.Copies) == 0 {
		v.fail("invalid_layout", "layout.copies", "at least one copy is required")
	}
	names := map[string]bool{}
	for _, formCopy := range layout.Copies {
		if names[formCopy.Name] {
			v.fail("invalid_layout", "layout.copies", fmt.Sprintf("copy %q is listed twice", formCopy.Name))
		}
		names[formCopy.Name] = true
		if len(formCopy.Pages) == 0 {
			v.fail("invalid_layout", "layout.copies."+formCopy.Name, "copy has no pages")
		}
		for _, page := range formCopy.Pages {
			if page < 0 || page >= v.a.Form.PDF.PageCount {
				v.fail("invalid_layout", "layout.copies."+formCopy.Name, fmt.Sprintf("page %d is outside the PDF (%d pages)", page, v.a.Form.PDF.PageCount))
			}
		}
	}
}

// checkGroup checks a field with a group, and the group's inner fields.
func (v *validator) checkGroup(field Field) {
	group := field.Group
	if field.Kind != "" || field.Source != "" || field.Template != "" || field.Format != nil || field.Style != nil ||
		field.Rect != (Rect{}) || field.Cells != 0 || field.CheckedValue != nil || field.Overflow != "" || field.Required {
		v.fail("invalid_group", field.ID, "a field with a group only takes id, label, page, copies and group; put the rest on its inner fields")
	}
	if group.Items == "" || len(group.Rows) == 0 || len(group.Fields) == 0 {
		v.fail("invalid_group", field.ID, "group needs items, at least one row and at least one field")
	}
	if group.Items != "" {
		v.checkPath(field.ID, group.Items)
	}
	v.checkCopies(field.ID, field.Copies, field.Page)

	innerIDs := map[string]bool{}
	for _, inner := range group.Fields {
		id := field.ID + "." + inner.ID
		if innerIDs[inner.ID] {
			v.fail("duplicate_id", id, "field id is not unique within group")
		}
		innerIDs[inner.ID] = true
		if inner.Group != nil {
			v.fail("invalid_group", id, "groups cannot be nested")
			continue
		}
		if inner.Page != 0 || len(inner.Copies) > 0 {
			v.fail("invalid_field", id, "page and copies are set on the group's field, not on its inner fields")
		}
		v.checkField(inner, id, field.Page, field.Copies)
	}
}

// checkField checks one box. page and copies are where it appears (a
// group's inner fields use the group's).
func (v *validator) checkField(field Field, id string, page int, copies []string) {
	if field.ID == "" {
		v.fail("invalid_field", id, "id is required")
	}
	v.checkCopies(id, copies, page)
	v.oneOf(id, "kind", field.Kind, KindText, KindMultiline, KindComb, KindCheckbox)
	v.oneOf(id, "overflow", field.Overflow, OverflowShrink, OverflowError)

	if (field.Source == "") == (field.Template == "") {
		v.fail("invalid_field", id, "exactly one of source or template is required")
	}
	if field.Source != "" {
		v.checkPath(id, field.Source)
	}
	for _, path := range templatePaths(field.Template) {
		v.checkPath(id, path)
	}

	switch field.Kind {
	case KindComb:
		if field.Cells < 1 {
			v.fail("invalid_field", id, "comb fields need cells >= 1")
		}
	case KindCheckbox:
		if field.Source == "" {
			v.fail("invalid_field", id, "checkboxes need a source")
		}
		if field.Format != nil {
			v.fail("invalid_field", id, "checkboxes do not take a format")
		}
	}
	if field.Kind != KindCheckbox && field.CheckedValue != nil {
		v.fail("invalid_field", id, "checkedValue only applies to checkboxes")
	}
	if field.Kind != KindComb && field.Cells != 0 {
		v.fail("invalid_field", id, "cells only applies to comb fields")
	}

	if field.Format != nil {
		v.checkFormat(id, *field.Format)
	}
	if field.Style != nil {
		v.checkStyle(id, *field.Style)
	}
	if field.Rect.Width <= 0 || field.Rect.Height <= 0 {
		v.fail("invalid_rect", id, "rect is required, with positive width and height")
		return
	}

	// Warn when even the smallest allowed font is taller than the box.
	style := EffectiveStyle(field, formatOf(field))
	usableHeight := field.Rect.Height - 2**style.Padding
	neededHeight := (capHeight + descent) * style.MinSize
	if neededHeight > usableHeight+tolerance {
		v.warn("text_too_tall", id, fmt.Sprintf("%.1fpt text needs %.1fpt of height; the box has %.1fpt after padding", style.MinSize, neededHeight, usableHeight))
	}
}

// checkCopies reports copy names that do not exist and pages that a copy
// does not have.
func (v *validator) checkCopies(id string, copies []string, page int) {
	for _, formCopy := range v.a.Layout.Copies {
		if appliesTo(copies, formCopy.Name) && page >= len(formCopy.Pages) {
			v.fail("invalid_page", id, fmt.Sprintf("page %d does not exist in copy %q", page, formCopy.Name))
		}
	}
	for _, name := range copies {
		if !v.copyExists(name) {
			v.fail("unknown_copy", id, fmt.Sprintf("copy %q is not defined in layout.copies", name))
		}
	}
}

func (v *validator) copyExists(name string) bool {
	for _, formCopy := range v.a.Layout.Copies {
		if formCopy.Name == name {
			return true
		}
	}
	return false
}

// checkFormat reports unknown values and options used with a format type
// they do not apply to.
func (v *validator) checkFormat(id string, format Format) {
	v.oneOf(id, "format type", format.Type, FormatText, FormatCurrency, FormatNumber, FormatSSN, FormatEIN, FormatDate)
	v.oneOf(id, "negative", format.Negative, "minus", "parens")
	v.oneOf(id, "zero", format.Zero, "show", "blank")
	v.oneOf(id, "part", format.Part, "integer", "fraction")

	isAmount := format.Type == FormatCurrency || format.Type == FormatNumber
	if !isAmount && (format.Decimals != nil || format.Negative != "" || format.Zero != "" || format.Part != "") {
		v.fail("invalid_format", id, "decimals, negative, zero and part only apply to currency and number")
	}
	if format.Decimals != nil && (*format.Decimals < 0 || *format.Decimals > 6) {
		v.fail("invalid_format", id, "decimals must be 0-6")
	}
	if format.Part == "fraction" && format.Type == FormatNumber && (format.Decimals == nil || *format.Decimals == 0) {
		v.fail("invalid_format", id, `part "fraction" needs decimals > 0`)
	}
	if format.Mask != nil {
		if format.Type != FormatSSN && format.Type != FormatEIN {
			v.fail("invalid_format", id, "mask only applies to ssn and ein")
		}
		if format.Mask.Visible < 0 || format.Mask.Visible > 9 {
			v.fail("invalid_format", id, "mask.visible must be 0-9")
		}
		for _, name := range format.Mask.Copies {
			if !v.copyExists(name) {
				v.fail("unknown_copy", id, fmt.Sprintf("mask copy %q is not defined in layout.copies", name))
			}
		}
	}
	if format.Pattern != "" && format.Type != FormatDate {
		v.fail("invalid_format", id, "pattern only applies to date")
	}
	if format.Upper && format.Type != "" && format.Type != FormatText {
		v.fail("invalid_format", id, "upper only applies to text")
	}
}

func (v *validator) checkStyle(id string, style Style) {
	if style.Size < 0 || style.MinSize < 0 || style.Padding != nil && *style.Padding < 0 {
		v.fail("invalid_style", id, "sizes must not be negative")
	}
	v.oneOf(id, "align", style.Align, AlignLeft, AlignCenter, AlignRight)
	v.oneOf(id, "valign", style.VAlign, VAlignTop, VAlignMiddle)
}

// checkGeometry places every rectangle (every copy, slot and group row) and
// checks that each lies on the page and that none overlap.
func (v *validator) checkGeometry() {
	type placedBox struct {
		id   string
		rect Rect
	}
	pageWidth, pageHeight := v.a.Layout.PageSize.Width, v.a.Layout.PageSize.Height
	for _, formCopy := range v.a.Layout.Copies {
		for pageIndex := range formCopy.Pages {
			var boxes []placedBox
			for slotIndex, slotOffset := range slotsOf(v.a) {
				slotTag := ""
				if len(v.a.Layout.Slots) > 1 {
					slotTag = "@slot" + strconv.Itoa(slotIndex)
				}
				for _, field := range v.a.Fields {
					if field.Page != pageIndex || !appliesTo(field.Copies, formCopy.Name) {
						continue
					}
					if field.Group == nil {
						boxes = append(boxes, placedBox{field.ID + slotTag, shift(field.Rect, slotOffset)})
						continue
					}
					for rowIndex, rowOffset := range field.Group.Rows {
						rowPosition := Offset{slotOffset.X + rowOffset.X, slotOffset.Y + rowOffset.Y}
						for _, inner := range field.Group.Fields {
							id := fmt.Sprintf("%s[%d].%s%s", field.ID, rowIndex, inner.ID, slotTag)
							boxes = append(boxes, placedBox{id, shift(inner.Rect, rowPosition)})
						}
					}
				}
			}
			for i, box := range boxes {
				r := box.rect
				if r.X < 0 || r.Y < 0 || r.X+r.Width > pageWidth || r.Y+r.Height > pageHeight {
					v.fail("off_page", box.id, fmt.Sprintf("rect %+v is outside the %gx%g page", r, pageWidth, pageHeight))
				}
				for _, other := range boxes[i+1:] {
					if overlap(r, other.rect) {
						v.fail("overlap", box.id, "overlaps "+other.id)
					}
				}
			}
		}
	}
}

// overlap reports whether two rectangles overlap by more than
// overlapTolerance; boxes that only share a border do not count.
func overlap(a, b Rect) bool {
	return a.X+overlapTolerance < b.X+b.Width && b.X+overlapTolerance < a.X+a.Width &&
		a.Y+overlapTolerance < b.Y+b.Height && b.Y+overlapTolerance < a.Y+a.Height
}

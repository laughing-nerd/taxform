package annotation

// SpecVersion is the version of the annotation format this package reads.
// Files with a different major version are rejected.
const SpecVersion = "1.0.0"

// Field kinds.
const (
	KindText      = "text"      // one line
	KindMultiline = "multiline" // wrapped lines, top-aligned by default
	KindComb      = "comb"      // one character per equal-width cell
	KindCheckbox  = "checkbox"  // prints a mark when the value is "checked"
)

// Format types.
const (
	FormatText     = "text"     // strings, numbers, booleans as-is
	FormatCurrency = "currency" // decimal amount, 2 decimals by default
	FormatNumber   = "number"   // decimal amount, 0 decimals by default
	FormatSSN      = "ssn"      // 9 digits as 123-45-6789 (also ITIN)
	FormatEIN      = "ein"      // 9 digits as 12-3456789
	FormatDate     = "date"     // ISO 8601 date (YYYY-MM-DD) re-patterned
)

// What to do when text does not fit its box.
const (
	OverflowShrink = "shrink" // default: smaller font down to minSize, then cut and warn
	OverflowError  = "error"  // print nothing and report an error
)

// Alignment inside the box.
const (
	AlignLeft    = "left"
	AlignCenter  = "center"
	AlignRight   = "right"
	VAlignTop    = "top"
	VAlignMiddle = "middle"
)

// Diagnostic severities.
const (
	SeverityError   = "error"
	SeverityWarning = "warning"
)

// Built-in style defaults, used for every key a field's style leaves unset
// (SPEC.md §8.1).
const (
	defaultFontSize    = 10.0 // points
	defaultMinFontSize = 6.0  // smallest size "shrink" may go down to
	defaultPadding     = 2.0  // points of space inside every edge of the box
	shrinkStep         = 0.5  // points removed per shrink step
	lineHeight         = 1.15 // multiline: distance between lines, × font size
	checkboxMark       = "X"  // printed in a checked checkbox
)

// Every value is printed in Courier, a PDF standard font. Because all its
// characters have the same width, every measurement below is plain
// arithmetic and gives identical results in any language.
//
// The numbers come from Adobe's Courier font metrics and are fractions of the
// font size: at 10pt, each character is 6pt wide.
const (
	fontName  = "Courier"
	charWidth = 0.600 // width of every character
	capHeight = 0.562 // height of capital letters above the baseline
	descent   = 0.157 // how far letters like "g" or "y" drop below the baseline
)

// tolerance absorbs tiny floating-point errors when comparing measurements:
// text measuring 36.0000000001pt still fits in a 36pt box.
const tolerance = 1e-9

// overlapTolerance lets neighbouring boxes share a border: rectangles that
// overlap by less than this many points only touch.
const overlapTolerance = 0.5

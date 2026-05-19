package pdf

// Version is the PDF specification version that the writer emits.
// Version selection is internal — boxesandglue users express intent
// via the Format enum, which maps to a Version internally.
type Version int

const (
	Version17 Version = 17 // ISO 32000-1:2008
	Version20 Version = 20 // ISO 32000-2:2017 (revised 2020)
)

// String returns the major.minor representation used in the %PDF- header.
func (v Version) String() string {
	switch v {
	case Version17:
		return "1.7"
	case Version20:
		return "2.0"
	}
	return "1.7"
}

// hasInfoDict reports whether this version still emits the document /Info
// dict. PDF 2.0 deprecates /Info in favour of XMP metadata.
func (v Version) hasInfoDict() bool {
	return v < Version20
}

// Package remarkable is a client for the reMarkable tablet's USB web interface
// (http://10.11.99.1 when the tablet is plugged in over USB and
// "USB web interface" is enabled in its storage settings).
package remarkable

import (
	"fmt"
	"time"
)

// Entry types returned by the tablet.
const (
	TypeDocument   = "DocumentType"
	TypeCollection = "CollectionType"
)

// Entry is a document or folder as listed by the tablet.
type Entry struct {
	ID          string `json:"ID"`
	VisibleName string `json:"VisibleName"`
	// Parent is the ID of the containing folder, "" for the root.
	Parent string `json:"Parent"`
	Type   string `json:"Type"`
	// ModifiedClient is the raw last-modification timestamp (RFC 3339).
	// It is kept as a string so it can be compared exactly.
	ModifiedClient string `json:"ModifiedClient"`
	// FileType is "notebook", "pdf" or "epub" for documents, empty for folders.
	FileType    string `json:"fileType,omitempty"`
	CurrentPage int    `json:"CurrentPage,omitempty"`
	Bookmarked  bool   `json:"Bookmarked"`
}

// IsFolder reports whether the entry is a folder (collection).
func (e Entry) IsFolder() bool { return e.Type == TypeCollection }

// Modified parses ModifiedClient. It returns the zero time if it is missing or invalid.
func (e Entry) Modified() time.Time {
	t, err := time.Parse(time.RFC3339Nano, e.ModifiedClient)
	if err != nil {
		return time.Time{}
	}
	return t
}

// Format is a download format supported by the web interface.
type Format string

const (
	// FormatPDF is a PDF rendering of the document, annotations included.
	FormatPDF Format = "pdf"
	// FormatRMDoc is the raw document archive (zip), lossless and re-importable.
	FormatRMDoc Format = "rmdoc"
)

// Ext returns the file extension for the format, with the leading dot.
func (f Format) Ext() string { return "." + string(f) }

// ParseFormat validates a format name.
func ParseFormat(s string) (Format, error) {
	switch f := Format(s); f {
	case FormatPDF, FormatRMDoc:
		return f, nil
	}
	return "", fmt.Errorf("unknown format %q (want %q or %q)", s, FormatPDF, FormatRMDoc)
}

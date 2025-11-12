package pdf

import (
	"bytes"
	"testing"
)

func TestNewPDFWriterWritesHeader(t *testing.T) {
	var buf bytes.Buffer
	pw := NewPDFWriter(&buf)
	// Minimal PDF content
	obj := pw.NewObject()
	obj.Data.WriteString("BT /F1 12 Tf ET")
	_ = pw.AddPage(obj, 0)
	if err := pw.FinishAndClose(); err != nil {
		t.Fatalf("Close error: %v", err)
	}
	out := buf.Bytes()
	if len(out) == 0 || out[0] != '%' || !bytes.Contains(out, []byte("xref")) || !bytes.Contains(out, []byte("trailer")) {
		t.Fatalf("output does not look like a PDF:\n%s", string(out[:min(200, len(out))]))
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

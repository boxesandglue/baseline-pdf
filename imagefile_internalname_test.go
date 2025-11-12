package pdf

import (
	"testing"
)

func TestInternalNameStable(t *testing.T) {
	img := &Imagefile{ // ggf. mit realen Feldern füllen
		Filename: "testdata/smiley.jpg",
	}
	a := img.InternalName()
	b := img.InternalName()
	if a != b {
		t.Fatalf("InternalName not stable: %s vs %s", a, b)
	}
}

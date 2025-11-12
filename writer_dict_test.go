package pdf

import (
	"strings"
	"testing"
)

func TestSerializeDictDeterministicOrder(t *testing.T) {
	d := Dict{
		"B": Name("/Bar"),
		"A": Name("/Foo"),
		"Z": Array{1},
	}
	s1 := serializeDict(d)
	s2 := serializeDict(d)
	if s1 != s2 {
		t.Fatalf("serializeDict not deterministic:\n%s\nvs\n%s", s1, s2)
	}
	// Expected Order: A, B, Z
	ai := strings.Index(s1, "/A")
	bi := strings.Index(s1, "/B")
	zi := strings.Index(s1, "/Z")
	if !(ai < bi && bi < zi) {
		t.Fatalf("unexpected key order in %q", s1)
	}
}

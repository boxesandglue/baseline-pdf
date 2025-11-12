package pdf

import "testing"

func TestNameStringAddsSlash(t *testing.T) {
	n := Name("AdobeGreen")
	if got := n.String(); got != "/AdobeGreen" {
		t.Fatalf("want /AdobeGreen, got %q", got)
	}
}

func TestNameStringKeepsSlash(t *testing.T) {
	n := Name("/Foo")
	if got := n.String(); got != "/Foo" {
		t.Fatalf("want /Foo, got %q", got)
	}
}

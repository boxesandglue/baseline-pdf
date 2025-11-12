//go:build go1.18

package pdf

import "testing"

// FuzzNameString checks that Name.String never panics or produces invalid output.
func FuzzNameString(f *testing.F) {
	f.Add("ExampleName") // optional seed corpus
	f.Fuzz(func(t *testing.T, s string) {
		_ = Name(s).String() // must not crash
	})
}

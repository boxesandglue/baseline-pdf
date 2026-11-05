package pdf

import "testing"

func TestNameEscapes(t *testing.T) {
	for _, tc := range []struct {
		name Name
		want string
	}{
		{"Plain", "/Plain"},
		{"/Slashed", "/Slashed"},
		{"HKS 43 K", "/HKS#2043#20K"},
		{"a#b", "/a#23b"},
		{"a(b)", "/a#28b#29"},
	} {
		if got := tc.name.String(); got != tc.want {
			t.Errorf("Name(%q) = %q, want %q", string(tc.name), got, tc.want)
		}
	}
}

package image

import "testing"

// TestResolveLink covers the two spellings a layer tar uses for a link target.
// Getting the relative case wrong sends /etc/os-release to etc/../usr/... and
// finds nothing.
func TestResolveLink(t *testing.T) {
	cases := []struct{ from, target, want string }{
		{"etc/os-release", "../usr/lib/os-release", "usr/lib/os-release"},
		{"etc/os-release", "/usr/lib/os-release", "usr/lib/os-release"},
		{"usr/bin/python3", "python3.12", "usr/bin/python3.12"},
		{"bin/sh", "/bin/dash", "bin/dash"},
	}
	for _, tc := range cases {
		if got := resolveLink(tc.from, tc.target); got != tc.want {
			t.Errorf("resolveLink(%q, %q) = %q, want %q", tc.from, tc.target, got, tc.want)
		}
	}
}

// TestWhiteoutFor pins the marker layout: a deleted file is recorded as
// .wh.<name> beside where it used to be.
func TestWhiteoutFor(t *testing.T) {
	cases := map[string]string{
		"etc/passwd": "etc/.wh.passwd",
		"app.yaml":   ".wh.app.yaml",
	}
	for in, want := range cases {
		if got := whiteoutFor(in); got != want {
			t.Errorf("whiteoutFor(%q) = %q, want %q", in, got, want)
		}
	}
}

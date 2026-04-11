package image

import (
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestIsImageIndex(t *testing.T) {
	tests := []struct {
		name  string
		input []byte
		want  bool
	}{
		{
			name:  "OCI image index",
			input: []byte(`{"mediaType":"application/vnd.oci.image.index.v1+json","schemaVersion":2,"manifests":[]}`),
			want:  true,
		},
		{
			name:  "Docker manifest list",
			input: []byte(`{"mediaType":"application/vnd.docker.distribution.manifest.list.v2+json","schemaVersion":2,"manifests":[]}`),
			want:  true,
		},
		{
			name:  "OCI single-arch manifest",
			input: []byte(`{"mediaType":"application/vnd.oci.image.manifest.v1+json","schemaVersion":2,"config":{},"layers":[]}`),
			want:  false,
		},
		{
			name:  "no mediaType but manifests with platform",
			input: []byte(`{"schemaVersion":2,"manifests":[{"platform":{"os":"linux","architecture":"amd64"}}]}`),
			want:  true,
		},
		{
			name:  "no mediaType manifests without platform",
			input: []byte(`{"schemaVersion":2,"manifests":[{"digest":"sha256:abc"}]}`),
			want:  false,
		},
		{
			name:  "invalid JSON",
			input: []byte(`not json`),
			want:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isImageIndex(tc.input)
			if got != tc.want {
				t.Errorf("isImageIndex() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestHostPlatform(t *testing.T) {
	p := hostPlatform()
	if p == "" {
		t.Fatal("hostPlatform() returned empty string")
	}
	// Must contain a slash (os/arch).
	found := false
	for _, c := range p {
		if c == '/' {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("hostPlatform() %q missing '/'", p)
	}
}

func TestParsePlatform(t *testing.T) {
	tests := []struct {
		input           string
		wantOS          string
		wantArch        string
		wantVariant     string
	}{
		{"linux/amd64", "linux", "amd64", ""},
		{"linux/arm64", "linux", "arm64", ""},
		{"linux/arm/v7", "linux", "arm", "v7"},
		{"windows/amd64", "windows", "amd64", ""},
	}
	for _, tc := range tests {
		os, arch, variant := parsePlatform(tc.input)
		if os != tc.wantOS || arch != tc.wantArch || variant != tc.wantVariant {
			t.Errorf("parsePlatform(%q) = (%q, %q, %q), want (%q, %q, %q)",
				tc.input, os, arch, variant, tc.wantOS, tc.wantArch, tc.wantVariant)
		}
	}
}

func TestMatchesPlatform(t *testing.T) {
	desc := func(os, arch, variant string) ocispec.Descriptor {
		return ocispec.Descriptor{
			Platform: &ocispec.Platform{OS: os, Architecture: arch, Variant: variant},
		}
	}

	tests := []struct {
		desc     ocispec.Descriptor
		platform string
		want     bool
	}{
		{desc("linux", "amd64", ""), "linux/amd64", true},
		{desc("linux", "amd64", ""), "linux/arm64", false},
		{desc("linux", "arm", "v7"), "linux/arm/v7", true},
		{desc("linux", "arm", "v7"), "linux/arm", true}, // variant not specified = match any
		{desc("linux", "arm", "v7"), "linux/arm/v6", false},
		{ocispec.Descriptor{}, "linux/amd64", false}, // nil platform
	}
	for _, tc := range tests {
		got := matchesPlatform(tc.desc, tc.platform)
		if got != tc.want {
			t.Errorf("matchesPlatform(%v, %q) = %v, want %v",
				tc.desc.Platform, tc.platform, got, tc.want)
		}
	}
}

func TestPlatformString(t *testing.T) {
	tests := []struct {
		p    *ocispec.Platform
		want string
	}{
		{nil, "unknown"},
		{&ocispec.Platform{OS: "linux", Architecture: "amd64"}, "linux/amd64"},
		{&ocispec.Platform{OS: "linux", Architecture: "arm", Variant: "v7"}, "linux/arm/v7"},
	}
	for _, tc := range tests {
		got := platformString(tc.p)
		if got != tc.want {
			t.Errorf("platformString(%v) = %q, want %q", tc.p, got, tc.want)
		}
	}
}

func TestPlatformFromConfig(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantOS      string
		wantArch    string
		wantVariant string
		wantErr     bool
	}{
		{
			name:     "full fields",
			input:    `{"os":"linux","architecture":"amd64"}`,
			wantOS:   "linux",
			wantArch: "amd64",
		},
		{
			name:        "arm with variant",
			input:       `{"os":"linux","architecture":"arm","variant":"v7"}`,
			wantOS:      "linux",
			wantArch:    "arm",
			wantVariant: "v7",
		},
		{
			name:     "defaults when empty",
			input:    `{}`,
			wantOS:   "linux",
			wantArch: "amd64",
		},
		{
			name:    "invalid JSON",
			input:   `not json`,
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, err := platformFromConfig([]byte(tc.input))
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if p.OS != tc.wantOS || p.Architecture != tc.wantArch || p.Variant != tc.wantVariant {
				t.Errorf("got (%q, %q, %q), want (%q, %q, %q)",
					p.OS, p.Architecture, p.Variant, tc.wantOS, tc.wantArch, tc.wantVariant)
			}
		})
	}
}

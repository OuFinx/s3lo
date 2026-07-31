package image

import (
	"strings"
	"testing"
)

// TestSelectPushPlatform covers the decision push makes when the daemon holds
// more than one platform. The case that matters is the third: it used to push
// one platform and say nothing, which is the whole of #101.
func TestSelectPushPlatform(t *testing.T) {
	multi := []string{"linux/amd64", "linux/arm64"}

	cases := []struct {
		name        string
		available   []string
		requested   string
		host        string
		wantChosen  string
		wantDropped []string
		wantErr     string
	}{
		{
			name: "classic image store reports nothing, push behaves as before",
			host: "linux/amd64",
		},
		{
			name: "single platform, nothing to report",
			available: []string{"linux/amd64"}, host: "linux/amd64",
			wantChosen: "linux/amd64",
		},
		{
			name:      "several platforms, host wins and the rest are named",
			available: multi, host: "linux/amd64",
			wantChosen: "linux/amd64", wantDropped: []string{"linux/arm64"},
		},
		{
			name:      "explicit request wins over the host",
			available: multi, requested: "linux/arm64", host: "linux/amd64",
			wantChosen: "linux/arm64", wantDropped: []string{"linux/amd64"},
		},
		{
			name:      "a variant is matched by its base platform",
			available: []string{"linux/arm/v7", "linux/amd64"}, requested: "linux/arm", host: "linux/amd64",
			wantChosen: "linux/arm", wantDropped: []string{"linux/amd64"},
		},
		{
			name:      "requesting a platform the image does not hold is an error",
			available: multi, requested: "windows/amd64", host: "linux/amd64",
			wantErr: "not in this image locally",
		},
		{
			name:      "no host platform among them means no silent guess",
			available: []string{"linux/arm64", "linux/s390x"}, host: "linux/amd64",
			wantErr: "pick one with --platform",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			chosen, dropped, err := selectPushPlatform(tc.available, tc.requested, tc.host)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want it to mention %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if chosen != tc.wantChosen {
				t.Errorf("chosen = %q, want %q", chosen, tc.wantChosen)
			}
			if strings.Join(dropped, ",") != strings.Join(tc.wantDropped, ",") {
				t.Errorf("dropped = %v, want %v", dropped, tc.wantDropped)
			}
		})
	}
}

// TestCheckExportedPlatform covers the post-condition on an export. Publishing
// one architecture's content under another's name is invisible afterwards: the
// bad tag deduplicates perfectly against the good one and reads back without
// error, so nothing downstream would ever notice.
func TestCheckExportedPlatform(t *testing.T) {
	cases := []struct {
		name, config, requested, wantErr string
	}{
		{
			name:   "exported platform matches",
			config: `{"os":"linux","architecture":"arm64"}`, requested: "linux/arm64",
		},
		{
			name:   "variant spelling differs but names the same platform",
			config: `{"os":"linux","architecture":"arm64"}`, requested: "linux/arm64/v8",
		},
		{
			name:   "the other spelling direction",
			config: `{"os":"linux","architecture":"arm64","variant":"v8"}`, requested: "linux/arm64",
		},
		{
			name:   "daemon ignored the request",
			config: `{"os":"linux","architecture":"amd64"}`, requested: "linux/arm64",
			wantErr: "but the daemon exported linux/amd64",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkExportedPlatform([]byte(tc.config), tc.requested)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("err = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

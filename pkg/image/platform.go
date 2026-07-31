package image

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// selectPushPlatform decides which platform a push publishes when the daemon
// holds more than one, and reports the ones being left behind.
//
// The default stays what it has always been — the host platform — because
// changing what an unqualified `s3lo push` publishes would be a surprise. What
// changes is that the platforms not published are now named, instead of
// vanishing without a word.
//
// available is empty on a daemon whose image store keeps one platform per tag,
// which is the common case; then there is nothing to choose or report.
func selectPushPlatform(available []string, requested, host string) (chosen string, dropped []string, err error) {
	if len(available) == 0 {
		return requested, nil, nil
	}

	if requested != "" {
		if !containsPlatform(available, requested) {
			return "", nil, fmt.Errorf("platform %q is not in this image locally (available: %s)",
				requested, strings.Join(sorted(available), ", "))
		}
		return requested, others(available, requested), nil
	}

	if len(available) == 1 {
		return available[0], nil, nil
	}

	if containsPlatform(available, host) {
		return host, others(available, host), nil
	}
	// The host platform is not among them, so any choice is arbitrary. Say so
	// rather than picking one silently.
	return "", nil, fmt.Errorf("image holds %s but not the host platform %s; pick one with --platform",
		strings.Join(sorted(available), ", "), host)
}

// containsPlatform matches a requested platform against what is available.
//
// The match is deliberately loose about the variant, in both directions:
// "linux/arm64" and "linux/arm64/v8" name the same thing, and which spelling
// appears depends on whether it came from a user, an image config or an index.
// Being strict here rejects requests that are plainly right.
func containsPlatform(available []string, want string) bool {
	for _, a := range available {
		if a == want || strings.HasPrefix(a, want+"/") || strings.HasPrefix(want, a+"/") {
			return true
		}
	}
	return false
}

func others(available []string, chosen string) []string {
	var rest []string
	for _, a := range available {
		if a == chosen || strings.HasPrefix(a, chosen+"/") {
			continue
		}
		rest = append(rest, a)
	}
	return sorted(rest)
}

func sorted(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// checkExportedPlatform verifies that what came back is the platform that was
// asked for.
//
// The daemon does refuse a platform it cannot produce, so this is a
// post-condition rather than a workaround: the failure it guards against —
// publishing one architecture's content under another's name — is invisible
// afterwards, because the bad tag deduplicates perfectly against the good one
// and reads back without error. Cheap check, silent failure.
func checkExportedPlatform(configData []byte, requested string) error {
	var cfg struct {
		OS           string `json:"os"`
		Architecture string `json:"architecture"`
		Variant      string `json:"variant"`
	}
	if err := json.Unmarshal(configData, &cfg); err != nil {
		return fmt.Errorf("parse image config to check platform: %w", err)
	}
	got := cfg.OS + "/" + cfg.Architecture
	if cfg.Variant != "" {
		got += "/" + cfg.Variant
	}
	if !containsPlatform([]string{got}, requested) {
		return fmt.Errorf("asked for platform %s but the daemon exported %s; "+
			"this image store holds one platform per tag, so there is nothing else to push",
			requested, got)
	}
	return nil
}

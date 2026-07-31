package image

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEvaluateTags_KeepLastIsRetentionFloor(t *testing.T) {
	now := time.Now()
	mk := func(tag string, ageDays int) tagMeta {
		return tagMeta{image: "app", tag: tag, lastModified: now.Add(-time.Duration(ageDays) * 24 * time.Hour)}
	}
	// Newest-first: v5(1d) v4(100d) v3(200d) v2(300d) v1(400d)
	tags := []tagMeta{mk("v5", 1), mk("v4", 100), mk("v3", 200), mk("v2", 300), mk("v1", 400)}

	names := func(ds []tagMeta) map[string]bool {
		m := map[string]bool{}
		for _, d := range ds {
			m[d.tag] = true
		}
		return m
	}

	t.Run("keep_last floor protects newest N even when old", func(t *testing.T) {
		// keep_last=3, max_age=90d. Without a floor, v4/v3 (older than 90d) would be
		// deleted; with the floor the newest 3 (v5,v4,v3) must survive, only v2,v1 go.
		del, err := evaluateTags(&LifecycleImageConfig{KeepLast: 3, MaxAge: "90d"}, tags)
		if err != nil {
			t.Fatal(err)
		}
		got := names(del)
		if got["v5"] || got["v4"] || got["v3"] {
			t.Fatalf("keep_last floor violated: %v", got)
		}
		if !got["v2"] || !got["v1"] {
			t.Fatalf("expected v1,v2 pruned by max_age, got %v", got)
		}
	})

	t.Run("keep_last alone prunes everything beyond N", func(t *testing.T) {
		del, err := evaluateTags(&LifecycleImageConfig{KeepLast: 2}, tags)
		if err != nil {
			t.Fatal(err)
		}
		got := names(del)
		if got["v5"] || got["v4"] {
			t.Fatalf("newest 2 must survive: %v", got)
		}
		if !got["v3"] || !got["v2"] || !got["v1"] {
			t.Fatalf("expected v3,v2,v1 pruned: %v", got)
		}
	})

	t.Run("max_age alone prunes by age", func(t *testing.T) {
		del, err := evaluateTags(&LifecycleImageConfig{MaxAge: "150d"}, tags)
		if err != nil {
			t.Fatal(err)
		}
		got := names(del)
		if got["v5"] || got["v4"] {
			t.Fatalf("young tags must survive: %v", got)
		}
		if !got["v3"] || !got["v2"] || !got["v1"] {
			t.Fatalf("expected tags older than 150d pruned: %v", got)
		}
	})
}

// TestApplyLifecycle_SkipsImmutableImages covers a data-loss bug: lifecycle was
// a third deletion path that never consulted the immutable flag. With
// `immutable: true` and `keep_last: 1`, `s3lo delete` correctly refused to
// remove a tag while `s3lo clean --confirm --tags` deleted four of them
// in the same store, with no --force and no warning.
func TestApplyLifecycle_SkipsImmutableImages(t *testing.T) {
	storeDir := t.TempDir()
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(storeDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldCwd)

	tags := []string{"v1", "v2", "v3", "v4", "v5"}
	for _, tag := range tags {
		dir := filepath.Join(".", "st", "manifests", "myapp", tag)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "manifest.json"),
			[]byte(`{"schemaVersion":2,"layers":[]}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := &BucketConfig{
		Default: ImageConfig{Lifecycle: &LifecycleImageConfig{KeepLast: 1}},
		Images:  map[string]ImageConfig{"myapp": {Immutable: boolPtr(true)}},
	}

	res, err := ApplyLifecycle(context.Background(), "local://./st/", cfg, false)
	if err != nil {
		t.Fatalf("ApplyLifecycle: %v", err)
	}
	if res.Deleted != 0 {
		t.Errorf("deleted %d tag(s) of an immutable image, want 0", res.Deleted)
	}
	if res.SkippedImmutable != 1 {
		t.Errorf("SkippedImmutable = %d, want 1", res.SkippedImmutable)
	}
	for _, tag := range tags {
		if _, err := os.Stat(filepath.Join(".", "st", "manifests", "myapp", tag, "manifest.json")); err != nil {
			t.Errorf("tag %s was deleted from an immutable image: %v", tag, err)
		}
	}

	// The guard must be immutability, not a blanket refusal to prune.
	cfg.Images["myapp"] = ImageConfig{Immutable: boolPtr(false)}
	res, err = ApplyLifecycle(context.Background(), "local://./st/", cfg, true)
	if err != nil {
		t.Fatalf("ApplyLifecycle (mutable, dry run): %v", err)
	}
	if res.Deleted != len(tags)-1 {
		t.Errorf("mutable image: would delete %d, want %d", res.Deleted, len(tags)-1)
	}
}

func boolPtr(b bool) *bool { return &b }

package image

import (
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

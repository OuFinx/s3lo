package image

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupSharingStore builds a bucket addressed through a team/ name filter, with
// three tags:
//
//	team/myapp:v1     -> layers L1, L2     (plain manifest)
//	team/myapp:v2     -> layers L1, L2     (image index -> one platform manifest)
//	team/other:latest -> layers L1, L3     (plain manifest)
//
// myapp:v2 is multi-arch on purpose: its platform manifest is a blob, and blobs
// live at the bucket root no matter which images the ref selects. A scan that
// treated the filter as a key prefix would look for it under team/ and lose both
// of that tag's layers.
func setupSharingStore(t *testing.T) string {
	t.Helper()

	parentDir := t.TempDir()
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(parentDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldCwd) })

	root := filepath.Join(parentDir, "sharestore")
	l1, l2, l3 := digestOf("a"), digestOf("b"), digestOf("c")
	child := digestOf("d")

	manifest := func(layers ...[2]any) []byte {
		var entries []string
		for _, l := range layers {
			entries = append(entries, fmt.Sprintf(
				`{"mediaType":"application/vnd.oci.image.layer.v1.tar+gzip","digest":"sha256:%s","size":%d}`,
				l[0], l[1]))
		}
		return fmt.Appendf(nil,
			`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json",`+
				`"config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:%s","size":10},`+
				`"layers":[%s]}`, digestOf("f"), strings.Join(entries, ","))
	}

	writeTestFile(t, filepath.Join(root, "manifests", "team", "myapp", "v1", "manifest.json"),
		manifest([2]any{l1, 100}, [2]any{l2, 200}))
	writeTestFile(t, filepath.Join(root, "manifests", "team", "other", "latest", "manifest.json"),
		manifest([2]any{l1, 100}, [2]any{l3, 50}))

	index := fmt.Appendf(nil,
		`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[`+
			`{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"sha256:%s","size":123,`+
			`"platform":{"os":"linux","architecture":"amd64"}},`+
			`{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"sha256:%s","size":99,`+
			`"platform":{"os":"unknown","architecture":"unknown"}}]}`, child, digestOf("e"))
	writeTestFile(t, filepath.Join(root, "manifests", "team", "myapp", "v2", "manifest.json"), index)
	writeTestFile(t, filepath.Join(root, "blobs", "sha256", child), manifest([2]any{l1, 100}, [2]any{l2, 200}))

	return "local://./sharestore/team/"
}

func TestLayerSharing_CountsSharedLayersAcrossTags(t *testing.T) {
	result, err := LayerSharing(context.Background(), setupSharingStore(t))
	if err != nil {
		t.Fatalf("LayerSharing: %v", err)
	}

	wantTags := []string{"team/myapp:v1", "team/myapp:v2", "team/other:latest"}
	if strings.Join(result.Tags, ",") != strings.Join(wantTags, ",") {
		t.Errorf("Tags = %v, want %v", result.Tags, wantTags)
	}

	got := make(map[string]SharedLayer, len(result.Layers))
	for _, l := range result.Layers {
		got[l.Digest] = l
	}

	tests := []struct {
		name     string
		digest   string
		wantSize int64
		wantTags []string
	}{
		{"shared by every tag", digestOf("a"), 100, []string{"team/myapp:v1", "team/myapp:v2", "team/other:latest"}},
		{"shared by both myapp tags", digestOf("b"), 200, []string{"team/myapp:v1", "team/myapp:v2"}},
		{"unique to one tag", digestOf("c"), 50, []string{"team/other:latest"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			layer, ok := got[tt.digest]
			if !ok {
				t.Fatalf("layer %s… missing from result", tt.digest[:12])
			}
			if layer.Size != tt.wantSize {
				t.Errorf("Size = %d, want %d", layer.Size, tt.wantSize)
			}
			if strings.Join(layer.Tags, ",") != strings.Join(tt.wantTags, ",") {
				t.Errorf("Tags = %v, want %v", layer.Tags, tt.wantTags)
			}
		})
	}

	if len(result.Layers) != 3 {
		t.Errorf("got %d unique layers, want 3", len(result.Layers))
	}
	// Most-shared layer sorts first so the CLI leads with it.
	if result.Layers[0].Digest != digestOf("a") {
		t.Errorf("first row = %s…, want the layer shared by all three tags", result.Layers[0].Digest[:12])
	}
	if result.StoredBytes != 350 {
		t.Errorf("StoredBytes = %d, want 350", result.StoredBytes)
	}
	if result.LogicalBytes != 750 {
		t.Errorf("LogicalBytes = %d, want 750", result.LogicalBytes)
	}
	if pct := result.DedupPercent(); pct < 53.3 || pct > 53.4 {
		t.Errorf("DedupPercent = %.2f, want ~53.33", pct)
	}
}

func TestLayerSharing_EmptyBucket(t *testing.T) {
	dir := t.TempDir()
	result, err := LayerSharing(context.Background(), "local://"+dir+"/")
	if err != nil {
		t.Fatalf("LayerSharing on empty bucket: %v", err)
	}
	if len(result.Tags) != 0 || len(result.Layers) != 0 || result.StoredBytes != 0 {
		t.Errorf("empty bucket reported content: %+v", result)
	}
}

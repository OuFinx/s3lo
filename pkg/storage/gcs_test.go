package storage

import "testing"

func TestGCSClientImplementsBackend(t *testing.T) {
	var _ Backend = (*GCSClient)(nil)
}

func TestToGCSStorageClass(t *testing.T) {
	tests := []struct {
		input StorageClass
		want  string
	}{
		{StorageClassStandard, "STANDARD"},
		{StorageClassIntelligentTiering, "NEARLINE"},
		{StorageClass("UNKNOWN"), ""},
		{StorageClass(""), ""},
	}
	for _, tt := range tests {
		got := toGCSStorageClass(tt.input)
		if got != tt.want {
			t.Errorf("toGCSStorageClass(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestGCSNotFoundError(t *testing.T) {
	err := &gcsNotFoundError{bucket: "my-bucket", key: "my-key"}
	want := "object not found: gs://my-bucket/my-key"
	if err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
	if !IsNotFound(err) {
		t.Error("IsNotFound should return true for gcsNotFoundError")
	}
}

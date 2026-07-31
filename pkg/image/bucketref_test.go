package image

import (
	"strings"
	"testing"
)

func TestParseBucketRootRef(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr string
	}{
		{
			name:  "bucket root",
			input: "s3://my-bucket/",
			want:  "my-bucket",
		},
		{
			name:    "rejects prefix",
			input:   "s3://my-bucket/some/prefix/",
			wantErr: "use s3://my-bucket/",
		},
		{
			name:    "rejects local prefix",
			input:   "local://./store/prefix/",
			wantErr: "use local://./store/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseBucketRootRef(tt.input)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ParseBucketRootRef() error = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseBucketRootRef() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("ParseBucketRootRef() = %q, want %q", got, tt.want)
			}
		})
	}
}

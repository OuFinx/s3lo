package ref

import (
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    Reference
		wantErr bool
	}{
		{
			name:  "full reference",
			input: "s3://my-bucket/myapp:v1.0",
			want:  Reference{Scheme: "s3", Bucket: "my-bucket", Image: "myapp", Tag: "v1.0"},
		},
		{
			name:  "nested path",
			input: "s3://my-bucket/org/myapp:v1.0",
			want:  Reference{Scheme: "s3", Bucket: "my-bucket", Image: "org/myapp", Tag: "v1.0"},
		},
		{
			name:  "no tag defaults to latest",
			input: "s3://my-bucket/myapp",
			want:  Reference{Scheme: "s3", Bucket: "my-bucket", Image: "myapp", Tag: "latest"},
		},
		{
			name:  "sha256 digest",
			input: "s3://my-bucket/myapp@sha256:abc123",
			want:  Reference{Scheme: "s3", Bucket: "my-bucket", Image: "myapp", Digest: "sha256:abc123"},
		},
		{
			name:  "local reference",
			input: "local://mystore/myapp:v1.0",
			want:  Reference{Scheme: "local", Bucket: "mystore", Image: "myapp", Tag: "v1.0"},
		},
		{
			name:  "local relative ./path",
			input: "local://./my-store/myapp:v1.0",
			want:  Reference{Scheme: "local", Bucket: "./my-store", Image: "myapp", Tag: "v1.0"},
		},
		{
			name:  "local relative ./path nested image",
			input: "local://./my-store/org/myapp:v1.0",
			want:  Reference{Scheme: "local", Bucket: "./my-store", Image: "org/myapp", Tag: "v1.0"},
		},
		{
			name:    "missing bucket",
			input:   "s3:///myapp:v1.0",
			wantErr: true,
		},
		{
			name:    "missing image",
			input:   "s3://my-bucket/",
			wantErr: true,
		},
		{
			name:    "invalid scheme",
			input:   "http://my-bucket/myapp:v1.0",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestReference_S3Prefix(t *testing.T) {
	ref := Reference{Bucket: "my-bucket", Image: "myapp", Tag: "v1.0"}
	want := "myapp/v1.0"
	if got := ref.S3Prefix(); got != want {
		t.Errorf("S3Prefix() = %q, want %q", got, want)
	}
}

func TestReference_String(t *testing.T) {
	ref := Reference{Scheme: "s3", Bucket: "my-bucket", Image: "myapp", Tag: "v1.0"}
	want := "s3://my-bucket/myapp:v1.0"
	if got := ref.String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestReference_StringLocal(t *testing.T) {
	ref := Reference{Scheme: "local", Bucket: "mystore", Image: "myapp", Tag: "v1.0"}
	want := "local://mystore/myapp:v1.0"
	if got := ref.String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

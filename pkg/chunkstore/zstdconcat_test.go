package chunkstore

import (
	"bytes"
	"math/rand"
	"testing"
)

// The whole compressed-serving design rests on this: zstd frames concatenated
// byte-for-byte must decode to the concatenation of their contents.
func TestZstdFramesConcatenate(t *testing.T) {
	parts := make([][]byte, 5)
	var whole, stream bytes.Buffer
	r := rand.New(rand.NewSource(7))
	for i := range parts {
		p := make([]byte, 1<<20)
		r.Read(p[:1<<19]) // half random, half zeros so it actually compresses
		parts[i] = p
		whole.Write(p)
		stream.Write(enc().EncodeAll(p, nil))
	}
	got, err := dec().DecodeAll(stream.Bytes(), nil)
	if err != nil {
		t.Fatalf("decoding concatenated frames: %v", err)
	}
	if !bytes.Equal(got, whole.Bytes()) {
		t.Fatalf("concatenated frames decoded to %d bytes, want %d", len(got), whole.Len())
	}
	t.Logf("%d frames, %d compressed bytes -> %d bytes", len(parts), stream.Len(), len(got))
}

package chunk

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"math/rand"
	"testing"
)

// testData builds a deterministic, incompressible-ish buffer. A fixed seed keeps
// chunk boundaries stable across runs so failures are reproducible.
func testData(t *testing.T, size int) []byte {
	t.Helper()
	b := make([]byte, size)
	r := rand.New(rand.NewSource(42))
	r.Read(b)
	return b
}

// randReader streams n deterministic bytes without materialising them, so the
// distribution test can cover hundreds of megabytes without the allocation.
type randReader struct {
	r    *rand.Rand
	left int
}

func newRandReader(seed int64, n int) *randReader {
	return &randReader{r: rand.New(rand.NewSource(seed)), left: n}
}

func (rr *randReader) Read(p []byte) (int, error) {
	if rr.left <= 0 {
		return 0, io.EOF
	}
	if len(p) > rr.left {
		p = p[:rr.left]
	}
	n, _ := rr.r.Read(p)
	rr.left -= n
	return n, nil
}

func splitAll(t *testing.T, data []byte) [][]byte {
	t.Helper()
	var chunks [][]byte
	err := Split(bytes.NewReader(data), func(c []byte) error {
		chunks = append(chunks, append([]byte(nil), c...))
		return nil
	})
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	return chunks
}

// splitDigests hashes each chunk in place, so large inputs do not need the
// chunk bytes retained.
func splitDigests(t *testing.T, r io.Reader) []string {
	t.Helper()
	var out []string
	err := Split(r, func(c []byte) error {
		sum := sha256.Sum256(c)
		out = append(out, hex.EncodeToString(sum[:]))
		return nil
	})
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	return out
}

func TestSplit_ReassemblesExactly(t *testing.T) {
	data := testData(t, 40<<20)
	var got bytes.Buffer
	for _, c := range splitAll(t, data) {
		got.Write(c)
	}
	if !bytes.Equal(got.Bytes(), data) {
		t.Fatalf("reassembled %d bytes, want %d, content differs", got.Len(), len(data))
	}
}

func TestSplit_RespectsSizeBounds(t *testing.T) {
	chunks := splitAll(t, testData(t, 40<<20))
	if len(chunks) < 2 {
		t.Fatalf("expected several chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		if len(c) > MaxSize {
			t.Errorf("chunk %d is %d bytes, over MaxSize %d", i, len(c), MaxSize)
		}
		// Only the final chunk may be shorter than MinSize.
		if i < len(chunks)-1 && len(c) < MinSize {
			t.Errorf("chunk %d is %d bytes, under MinSize %d", i, len(c), MinSize)
		}
	}
}

// TestSplit_AverageSizeNearTarget guards the mask bit counts. Masks that are too
// loose collapse the average onto MinSize, which still resynchronises but stores
// several times more objects than intended; masks that are too strict push every
// chunk to MaxSize and coarsen deduplication.
func TestSplit_AverageSizeNearTarget(t *testing.T) {
	const total = 512 << 20

	count := 0
	err := Split(newRandReader(7, total), func(c []byte) error {
		count++
		return nil
	})
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	if count == 0 {
		t.Fatal("no chunks produced")
	}

	avg := total / count
	lo, hi := AvgSize/2, AvgSize*2
	t.Logf("average chunk size %.2f MB over %d chunks (target %d MB)",
		float64(avg)/(1<<20), count, AvgSize>>20)
	if avg < lo || avg > hi {
		t.Fatalf("average chunk size %d is outside [%d, %d]; check maskS/maskL bit counts",
			avg, lo, hi)
	}
}

func TestSplit_Deterministic(t *testing.T) {
	data := testData(t, 40<<20)
	a := splitDigests(t, bytes.NewReader(data))
	b := splitDigests(t, bytes.NewReader(data))
	if len(a) != len(b) {
		t.Fatalf("chunk count differs between runs: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("chunk %d differs between runs on identical input", i)
		}
	}
}

// TestSplit_ResynchronizesAfterInsert is the property that makes content-defined
// chunking worth its cost. Inserting bytes in the middle must disturb only the
// chunks around the edit; fixed-size chunking would shift every boundary after
// the insert and fail this test.
func TestSplit_ResynchronizesAfterInsert(t *testing.T) {
	data := testData(t, 160<<20)
	before := splitDigests(t, bytes.NewReader(data))
	if len(before) < 8 {
		t.Fatalf("need enough chunks for this to mean anything, got %d", len(before))
	}

	// Insert a few bytes near the middle of the stream.
	mid := len(data) / 2
	edited := make([]byte, 0, len(data)+8)
	edited = append(edited, data[:mid]...)
	edited = append(edited, []byte("INSERTED")...)
	edited = append(edited, data[mid:]...)
	after := splitDigests(t, bytes.NewReader(edited))

	shared := map[string]int{}
	for _, d := range before {
		shared[d]++
	}
	common := 0
	for _, d := range after {
		if shared[d] > 0 {
			shared[d]--
			common++
		}
	}

	// An 8-byte insert should invalidate the chunk containing it and, at worst,
	// its neighbour once the boundary jitters. Everything else must be reused.
	minShared := len(before) - 2
	if common < minShared {
		t.Fatalf("only %d of %d chunks survived an 8-byte insert (want >= %d); "+
			"boundaries are not resynchronizing", common, len(before), minShared)
	}
	t.Logf("%d of %d chunks reused after insert (%d chunks after edit)",
		common, len(before), len(after))
}

func TestSplit_EmptyStream(t *testing.T) {
	calls := 0
	err := Split(bytes.NewReader(nil), func([]byte) error { calls++; return nil })
	if err != nil {
		t.Fatalf("Split on empty stream: %v", err)
	}
	if calls != 0 {
		t.Fatalf("expected no chunks for an empty stream, got %d", calls)
	}
}

func TestSplit_SmallerThanMinSize(t *testing.T) {
	data := testData(t, 1024)
	chunks := splitAll(t, data)
	if len(chunks) != 1 {
		t.Fatalf("expected a single short chunk, got %d", len(chunks))
	}
	if !bytes.Equal(chunks[0], data) {
		t.Fatal("short chunk does not match input")
	}
}

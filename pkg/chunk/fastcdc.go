// Package chunk splits a byte stream into content-defined chunks.
//
// Container registries deduplicate at layer granularity because the OCI
// Distribution Spec addresses layers, not their contents: change one file in a
// 2 GB layer and the whole layer is a new blob. Content-defined chunking cuts
// the stream at boundaries chosen by a rolling hash of the surrounding bytes, so
// an edit only disturbs the chunks it actually touches — everything before and
// after keeps its old boundaries and stays deduplicated.
//
// The algorithm is FastCDC (Xia et al., USENIX ATC '16) with normalized
// chunking: a stricter mask below the average size and a looser one above it,
// which pulls the size distribution towards the average and away from the
// min/max cutoffs.
package chunk

import (
	"errors"
	"fmt"
	"io"
)

// Chunking must stay byte-for-byte reproducible forever: if these values or the
// gear table below ever change, previously written chunks stop matching newly
// written ones and every bucket silently loses its deduplication. Treat any
// change here as a new storage format, not a tweak.
const (
	// MinSize is the smallest chunk the splitter will emit (except the last).
	MinSize = 1 << 20 // 1 MB
	// AvgSize is the target average chunk size.
	AvgSize = 4 << 20 // 4 MB
	// MaxSize is the hard cut-off, applied even when no boundary is found.
	MaxSize = 16 << 20 // 16 MB
)

// Masks for normalized chunking. maskS has more bits set than maskL, so a
// boundary is harder to hit before AvgSize and easier after it.
//
// A mask with n bits set makes a boundary land every 2^n bytes on average, so
// the bit counts are chosen against AvgSize-MinSize (3 MB, close to 2^21.5)
// rather than against AvgSize itself: the scan does not start looking for a cut
// until MinSize bytes have already been consumed. The set bits are spread across
// the word instead of clustered low, because h = (h<<1)+gear[b] leaves the
// lowest bits dominated by the single most recent byte.
const (
	maskS = 0x14AA_54AA_54AA_5000 // 22 bits set
	maskL = 0x1294_A529_4A52_9000 // 20 bits set
)

// gear is the FastCDC gear table: one pseudo-random 64-bit value per input byte
// value. It is generated deterministically with splitmix64 so the table is
// identical on every platform and every build without shipping a 256-entry
// literal.
var gear [256]uint64

func init() {
	x := uint64(0x9E3779B97F4A7C15)
	for i := range gear {
		x += 0x9E3779B97F4A7C15
		z := x
		z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
		z = (z ^ (z >> 27)) * 0x94D049BB133111EB
		gear[i] = z ^ (z >> 31)
	}
}

// ErrShortRead is returned when the source ends mid-chunk in a way that cannot
// be distinguished from a truncated stream.
var ErrShortRead = errors.New("chunk: source ended unexpectedly")

// Split reads r to EOF and calls fn once per chunk, in order. The slice passed
// to fn is only valid for the duration of the call; copy it to retain the data.
// A nil or empty stream produces no calls.
func Split(r io.Reader, fn func(data []byte) error) error {
	buf := make([]byte, MaxSize)
	filled := 0

	for {
		// Top up the window so a full MaxSize chunk is always available to scan
		// unless the stream is genuinely exhausted.
		n, err := io.ReadFull(r, buf[filled:])
		filled += n
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
			return fmt.Errorf("chunk: read source: %w", err)
		}
		atEOF := errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)

		if filled == 0 {
			return nil
		}

		cut := boundary(buf[:filled])
		if err := fn(buf[:cut]); err != nil {
			return err
		}

		remaining := copy(buf, buf[cut:filled])
		filled = remaining

		if atEOF && filled == 0 {
			return nil
		}
		if atEOF {
			// Flush what is left, splitting again if it still exceeds MaxSize.
			for filled > 0 {
				cut := boundary(buf[:filled])
				if err := fn(buf[:cut]); err != nil {
					return err
				}
				filled = copy(buf, buf[cut:filled])
			}
			return nil
		}
	}
}

// boundary returns the length of the first chunk in data, which is at most
// len(data). Bytes before MinSize are not considered as cut points, so the
// rolling hash is only consulted once a chunk is large enough to be worth
// ending.
func boundary(data []byte) int {
	n := len(data)
	if n <= MinSize {
		return n
	}
	if n > MaxSize {
		n = MaxSize
	}

	normal := AvgSize
	if n < normal {
		normal = n
	}

	var h uint64
	// Strict mask up to the average size: boundaries are rarer, so chunks are
	// pushed towards AvgSize rather than ending just past MinSize.
	i := MinSize
	for ; i < normal; i++ {
		h = (h << 1) + gear[data[i]]
		if h&maskS == 0 {
			return i + 1
		}
	}
	// Relaxed mask past the average: boundaries become more likely, so few
	// chunks run all the way to MaxSize.
	for ; i < n; i++ {
		h = (h << 1) + gear[data[i]]
		if h&maskL == 0 {
			return i + 1
		}
	}
	return n
}

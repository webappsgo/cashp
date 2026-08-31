package backup

import "math/bits"

// Content-defined chunking parameters. Boundaries are chosen by a rolling
// buzhash over a fixed window, so inserting or removing bytes in one file
// shifts only the affected chunk instead of every chunk after it. That is
// what lets successive backups share the overwhelming majority of their
// blocks and keeps stored size a small fraction of primary storage.
const (
	// chunkWindow is the rolling-hash window in bytes.
	chunkWindow = 48
	// chunkMin is the smallest chunk a boundary may produce.
	chunkMin = 2 << 10
	// chunkMask selects a boundary on average every 8 KiB.
	chunkMask = (1 << 13) - 1
	// chunkMax is the largest chunk allowed before a boundary is forced.
	chunkMax = 64 << 10
)

// buzTable maps a byte to its random 64-bit value; buzOut holds the same
// values pre-rotated by the window length so a byte leaving the window can
// be removed with a single xor.
var buzTable, buzOut = newBuzTables()

// newBuzTables derives both hash tables from a fixed splitmix64 seed, so
// chunk boundaries are identical on every machine and every build.
func newBuzTables() ([256]uint64, [256]uint64) {
	var table, out [256]uint64

	state := uint64(0x9E3779B97F4A7C15)
	for i := 0; i < 256; i++ {
		state += 0x9E3779B97F4A7C15
		z := state
		z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
		z = (z ^ (z >> 27)) * 0x94D049BB133111EB
		z ^= z >> 31
		table[i] = z
		out[i] = bits.RotateLeft64(z, chunkWindow%64)
	}

	return table, out
}

// splitBlocks divides data into content-defined chunks. The returned
// slices are views into data and must not be modified by the caller.
func splitBlocks(data []byte) [][]byte {
	if len(data) == 0 {
		return nil
	}

	var out [][]byte

	start := 0
	var h uint64

	for i := 0; i < len(data); i++ {
		h = bits.RotateLeft64(h, 1) ^ buzTable[data[i]]
		if i-start >= chunkWindow {
			h ^= buzOut[data[i-chunkWindow]]
		}

		size := i - start + 1
		if size < chunkMin {
			continue
		}

		if size >= chunkMax || h&chunkMask == 0 {
			out = append(out, data[start:i+1])
			start = i + 1
			h = 0
		}
	}

	if start < len(data) {
		out = append(out, data[start:])
	}

	return out
}

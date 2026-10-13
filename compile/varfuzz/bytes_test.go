//go:build linux && (amd64 || arm64 || riscv64 || ppc64 || ppc64le || 386)

package varfuzz

import "encoding/binary"

// choices is also used by the offline legacy-corpus converter. Fuzz targets
// always use byteChoices: changing input bytes changes individual AST decisions.
type choices interface {
	Intn(int) int
	Uint64() uint64
	Shuffle(int, func(int, int))
}

type byteChoices struct {
	data   []byte
	offset int
}

func (r *byteChoices) Uint64() uint64 {
	var b [8]byte
	if r.offset < len(r.data) {
		r.offset += copy(b[:], r.data[r.offset:])
	}
	return binary.LittleEndian.Uint64(b[:])
}
func (r *byteChoices) Intn(n int) int { return int(r.Uint64() % uint64(n)) }
func (r *byteChoices) Shuffle(n int, swap func(int, int)) {
	for i := n - 1; i > 0; i-- {
		swap(i, r.Intn(i+1))
	}
}

// sweepBytes provides a stable, architecture-independent fixed sweep. The AST
// decoder is identical to the coverage-guided fuzz target's decoder.
func sweepBytes(index uint64) []byte {
	data := make([]byte, 32768)
	state := index ^ 0x9e3779b97f4a7c15
	for i := 0; i < len(data); i += 8 {
		state += 0x9e3779b97f4a7c15
		x := state
		x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
		x = (x ^ (x >> 27)) * 0x94d049bb133111eb
		binary.LittleEndian.PutUint64(data[i:], x^(x>>31))
	}
	return data
}

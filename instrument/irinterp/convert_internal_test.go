package irinterp

import (
	"math"
	"math/big"
	"math/rand"
	"testing"
)

// Binary32 results from 64-bit integers round once, including on hosts
// whose runtime conversion does not (Go's 386 runtime).
func TestFromIntegerBinary32RoundsOnce(t *testing.T) {
	values := []uint64{0, 1, 0xb017c8eea708, 1 << 63, 1<<64 - 1, 1<<53 + 1, 1<<24 + 1, 0xffffff8000000000}
	r := rand.New(rand.NewSource(1))
	for range 100000 {
		values = append(values, r.Uint64()>>uint(r.Intn(64)))
	}
	for _, x := range values {
		su, _ := new(big.Float).SetUint64(x).Float32()
		ss, _ := new(big.Float).SetInt64(int64(x)).Float32()
		if got := fromInteger(false, x, 64, 32); got != uint64(math.Float32bits(su)) {
			t.Fatalf("unsigned %#x: got %#x, want %#x", x, got, math.Float32bits(su))
		}
		if got := fromInteger(true, x, 64, 32); got != uint64(math.Float32bits(ss)) {
			t.Fatalf("signed %#x: got %#x, want %#x", x, got, math.Float32bits(ss))
		}
	}
}

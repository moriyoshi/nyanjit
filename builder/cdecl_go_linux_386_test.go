//go:build linux && 386

package builder_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/builder"
)

func Test386CallGoSubwordRules(t *testing.T) {
	for _, tc := range []struct {
		bits   []uint8
		reject bool
	}{
		{[]uint8{8, 16, 8, 32}, true}, {[]uint8{8, 8}, true},
		{[]uint8{16, 16}, true}, {[]uint8{16, 8}, true},
		{[]uint8{32, 8, 16}, true}, {[]uint8{8}, false},
		{[]uint8{16}, false}, {[]uint8{8, 32, 16}, false},
		{[]uint8{16, 64, 8}, false},
	} {
		for _, goCall := range []bool{true, false} {
			b := builder.NewBuilder()
			target, err := b.ConstNative(1)
			if err != nil {
				t.Fatal(err)
			}
			args := make([]builder.Value, len(tc.bits))
			for i, w := range tc.bits {
				args[i], err = b.ConstBits(w, 1)
				if err != nil {
					t.Fatal(err)
				}
			}
			if goCall {
				_, err = b.CallGo(target, args...)
			} else {
				_, err = b.CallC(target, args...)
			}
			if goCall && tc.reject {
				if !errors.Is(err, builder.ErrBackendLimit) || !strings.Contains(err.Error(), "packed Go ABI0 subword slots") {
					t.Fatalf("Go args%v: %v", tc.bits, err)
				}
			} else if err != nil {
				t.Fatalf("goCall%v args%v: %v", goCall, tc.bits, err)
			}
		}
	}
}

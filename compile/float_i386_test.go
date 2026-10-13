package compile_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
)

func Test386CrossAssemblySSE2Baseline(t *testing.T) {
	for _, cpu := range []abi.CPUProfile{{}, {Extensions: []string{"sse2"}}} {
		for _, width := range []uint8{32, 64} {
			b := nyanjit.NewBuilder()
			x, y := b.ParamBits(width), b.ParamBits(width)
			// Contract must not introduce FMA on the SSE2-only target.
			b.Return(x.FMul(y).FAdd(x))
			p := finishCrossAssembly(t, b)
			text, err := lowerListing(p, compile.TargetSpec{Architecture: "386", CPU: cpu}, compile.Func{})
			if err != nil {
				t.Fatal(err)
			}
			suffix := "ss"
			if width == 64 {
				suffix = "sd"
			}
			for _, mnemonic := range []string{"mul" + suffix, "add" + suffix} {
				if !strings.Contains(text, mnemonic) {
					t.Fatalf("width%d missing %s:\n%s", width, mnemonic, text)
				}
			}
			if strings.Contains(text, "fmadd") || strings.Contains(text, "literal") {
				t.Fatalf("unexpected FMA/literal lowering:\n%s", text)
			}
		}
	}
}

func Test386ExplicitFMAUnavailable(t *testing.T) {
	for _, width := range []uint8{32, 64} {
		b := nyanjit.NewBuilder()
		x, y, z := b.ParamBits(width), b.ParamBits(width), b.ParamBits(width)
		b.Return(b.FMA(x, y, z))
		_, err := lowerListing(finishCrossAssembly(t, b), compile.TargetSpec{Architecture: "386"}, compile.Func{})
		if !errors.Is(err, compile.ErrBackendLimit) || !strings.Contains(err.Error(), "FMA") {
			t.Fatalf("FMA%d: %v", width, err)
		}
	}
}

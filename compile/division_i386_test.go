package compile_test

import (
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// Test386DivisionAndConversionListings lowers every division and
// conversion for 386, with a platform stack and without one (a C entry then
// saves the preserved registers a 64-bit division clobbers in its
// workspace), and checks the listing shows the expansion.
func Test386DivisionAndConversionListings(t *testing.T) {
	type form struct {
		name     string
		build    func(b *nyanjit.Builder)
		mnemonic string
	}
	var forms []form
	for _, w := range []uint8{8, 16, 32, 64} {
		for _, s := range []ir.DivisionSemantics{ir.DivisionZero, ir.DivisionAllOnes, ir.DivisionUnchecked} {
			for _, op := range []struct {
				name string
				f    func(b *nyanjit.Builder, s ir.DivisionSemantics, x, y nyanjit.Value) nyanjit.Value
			}{
				{"div", (*nyanjit.Builder).DivWith}, {"divs", (*nyanjit.Builder).DivSignedWith},
				{"rem", (*nyanjit.Builder).RemWith}, {"rems", (*nyanjit.Builder).RemSignedWith},
			} {
				forms = append(forms, form{op.name, func(b *nyanjit.Builder) {
					b.Return(op.f(b, s, b.ParamBits(w), b.ParamBits(w)))
				}, "div "})
			}
		}
	}
	for _, in := range []uint8{32, 64} {
		// A pair converts through memory operands.
		from := map[uint8]string{32: "cvtsi2s", 64: "addsd xmm"}[in]
		for _, out := range []uint8{32, 64} {
			forms = append(forms,
				form{"ffroms", func(b *nyanjit.Builder) { b.Return(b.FFromSigned(b.ParamBits(in), out)) }, from},
				form{"ffromu", func(b *nyanjit.Builder) { b.Return(b.FFromUnsigned(b.ParamBits(in), out)) }, from},
				form{"ftos", func(b *nyanjit.Builder) { b.Return(b.FToSigned(b.ParamBits(in), out)) }, "ucomis"},
				form{"ftou", func(b *nyanjit.Builder) {
					b.Return(b.FToUnsignedWith(ir.ConversionNative, b.ParamBits(in), out))
				}, "ucomis"})
		}
	}
	for _, f := range forms {
		b := nyanjit.NewBuilder()
		f.build(b)
		p := finishCrossAssembly(t, b)
		for _, stack := range []compile.StackPolicy{{Practice: abi.StackPlatform, BytesAvailable: 1 << 12}, {}} {
			text, err := lowerListing(p, compile.TargetSpec{Architecture: "386"}, compile.Func{Stack: stack})
			if err != nil {
				t.Fatalf("%s %+v: %v", f.name, stack, err)
			}
			if !strings.Contains(text, f.mnemonic) {
				t.Fatalf("%s %+v: no %q in\n%s", f.name, stack, f.mnemonic, text)
			}
		}
	}
}

package compile_test

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
)

func TestConstantMultiplyUsesAddressArithmetic(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64", "386"} {
		for _, factor := range []uint64{3, 5, 9} {
			for _, reverse := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%d/reverse=%t", arch, factor, reverse), func(t *testing.T) {
					width := uint8(64)
					if arch == "386" {
						width = 32
					}
					b := n.NewBuilder()
					x := b.ParamBits(width)
					c := b.ConstBits(width, factor)
					left, right := x, c
					if reverse {
						left, right = c, x
					}
					product := b.Mul(left, right)
					b.Return(b.Add(product, b.ConstBits(width, 1)))
					p, err := b.Finish()
					if err != nil {
						t.Fatal(err)
					}
					listing, err := lowerListing(p, compile.TargetSpec{Architecture: arch}, compile.Func{ABI: abi.C})
					if err != nil {
						t.Fatal(err)
					}
					if strings.Contains(listing, "mul") || (arch != "arm64" && !strings.Contains(listing, "lea")) || (arch == "arm64" && !strings.Contains(listing, "lsl")) {
						t.Fatalf("expected address arithmetic for constant multiply:\n%s", listing)
					}
				})
			}
		}
	}
}

func TestConstantMultiplyWraparound(t *testing.T) {
	if runtime.GOOS != "linux" || (runtime.GOARCH != "arm64" && runtime.GOARCH != "amd64") {
		t.Skip("native 64-bit execution")
	}
	for _, width := range []uint8{8, 16, 32, 64} {
		for _, factor := range []uint64{3, 5, 9, 17, 1<<63 + 1} {
			if width < 64 && factor > 1<<width-1 {
				continue
			}
			for _, harden := range []bool{false, true} {
				t.Run(fmt.Sprintf("%d/%d/harden=%t", width, factor, harden), func(t *testing.T) {
					target, err := compile.NewTarget(compile.TargetSpec{})
					if err != nil {
						t.Fatal(err)
					}
					b := n.NewBuilderFor(target, compile.Func{ABI: abi.C, Context: compile.ContextForeign, Harden: harden, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}})
					x := b.ParamBits(width)
					b.Return(b.Add(b.Mul(x, b.ConstBits(width, factor)), b.ConstBits(width, 1)))
					u, err := b.Lower()
					if err != nil {
						t.Fatal(err)
					}
					tmpl, err := u.Template()
					if err != nil {
						t.Fatal(err)
					}
					code, err := tmpl.Bind(nil)
					if err != nil {
						t.Fatal(err)
					}
					defer code.Close()
					mask := ^uint64(0) >> (64 - width)
					for _, x := range []uint64{0, 1, 2, mask / 2, mask/2 + 1, mask - 1, mask} {
						want := (x*factor + 1) & mask
						got, err := code.Call(uintptr(x))
						if err != nil || uint64(got) != want {
							t.Fatalf("f(%#x)=%#x,%v; want %#x", x, got, err, want)
						}
					}
				})
			}
		}
	}
}

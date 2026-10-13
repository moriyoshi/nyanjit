//go:build linux && 386

package compile_test

import (
	"encoding/binary"
	"fmt"
	"runtime"
	"testing"
	"unsafe"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
)

func Test386PairedAddressMemory(t *testing.T) {
	for _, stack := range []abi.StackPractice{abi.StackNoUse, abi.StackPlatform} {
		for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
			t.Run(fmt.Sprintf("stack%d/abi%d", stack, flavor), func(t *testing.T) {
				b := n.NewBuilder()
				address, value := b.ParamBits(64), b.ParamBits(64)
				b.Store64(address, value)
				b.Return(b.Xor(b.Load64(address), value)) // keep the source live through the store/load
				p, err := testFinishRaw(b)
				if err != nil {
					t.Fatal(err)
				}
				opts := compile.Func{ABI: flavor, Stack: compile.StackPolicy{Practice: stack}}
				if stack == abi.StackPlatform {
					opts.Stack.BytesAvailable = 8192
				}
				c, err := n.Compile(p, compile.TargetSpec{}, opts)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				data := []byte{0xa5, 0, 0, 0, 0, 0, 0, 0, 0, 0x5a}
				var pin runtime.Pinner
				pin.Pin(&data[0])
				defer pin.Unpin()
				for _, value := range []uint64{0, 1, 0x123456789abcdef0, ^uint64(0)} {
					lo, hi, err := c.CallPair(execution.Words(uint64(uintptr(unsafe.Pointer(&data[1]))), value)...)
					if err != nil || execution.Wide(lo, hi) != 0 || binary.LittleEndian.Uint64(data[1:9]) != value || data[0] != 0xa5 || data[9] != 0x5a {
						t.Fatalf("value%x got%x memory%x err%v", value, execution.Wide(lo, hi), data, err)
					}
				}
			})
		}
	}
}

package compile_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
)

//go:noinline
func managedFrameCallee(a uint64) uint64 { return a + 1 }

// A managed amd64 frame's top word is where Go frames save BP, and a stack
// copy adjusts it as a saved BP, so no spill or home may use it (security
// audit 2026-10-25, L15).
func TestAMD64ManagedFramesLeaveTopWord(t *testing.T) {
	target, err := compile.NewTarget(compile.TargetSpec{Architecture: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
		for count := 0; count <= 40; count++ {
			b := n.NewBuilder()
			x := b.Param()
			iv := x
			var live []n.Value
			for i := range count {
				iv = b.Add(b.Xor(iv, x), b.Const(uint64(i+9)))
				live = append(live, iv)
			}
			sum := b.CallGo(b.ConstNative(reflect.ValueOf(managedFrameCallee).Pointer()), iv)
			for _, v := range live {
				sum = b.Add(sum, v)
			}
			b.Return(sum)
			p, err := b.Finish()
			if err != nil {
				t.Fatal(err)
			}
			unit, err := compile.Lower(target, p, compile.Func{ABI: flavor, Context: compile.ContextGoManaged,
				Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1 << 16}, AssumePointerFree: true})
			if err != nil {
				t.Fatal(err)
			}
			listing, err := unit.Listing()
			if err != nil {
				t.Fatal(err)
			}
			var frame int
			if _, err := fmt.Sscanf(strings.SplitN(listing, "\n", 2)[0], "0000  sub RSP, %d", &frame); err != nil {
				t.Fatalf("%v %d: no frame in %q", flavor, count, strings.SplitN(listing, "\n", 2)[0])
			}
			if top := fmt.Sprintf("[RSP + %d]", frame-8); strings.Contains(listing, top) {
				t.Fatalf("%v %d live values: frame %d uses its top word %s\n%s", flavor, count, frame, top, listing)
			}
		}
	}
}

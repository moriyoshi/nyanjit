package compile

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
)

func TestSecurityARM64SpillCapacity(t *testing.T) {
	for _, tt := range []struct {
		flavor  abi.Flavor
		managed bool
		limit   int
	}{
		{abi.C, false, 4095}, {abi.Go, false, 4086}, {abi.C, true, 4069},
	} {
		for _, slots := range []int{tt.limit, tt.limit + 1} {
			context := ContextNative
			if tt.managed {
				context = ContextGoManaged
			}
			e := new(physicalEmitter)
			initARM64MachineEmitter(e, uncheckedTarget(TargetSpec{}).options(Func{ABI: tt.flavor, Context: context, Stack: StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1 << 20}}))
			_, err := e.finish(0, slots)
			if slots == tt.limit && err != nil {
				t.Fatalf("valid slots=%d: %v", slots, err)
			}
			if slots > tt.limit && !errors.Is(err, ErrBackendLimit) {
				t.Fatalf("ABI %v managed=%v slots=%d accepted: %v", tt.flavor, tt.managed, slots, err)
			}
		}
	}
}

//go:build nyanjit_no_goregister && linux && (amd64 || arm64 || 386 || riscv64 || loong64 || ppc64le)

package compile

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestSecurityManagedCompilationDisabled(t *testing.T) {
	p := &ir.Program{Instructions: []ir.Instruction{{Op: ir.OpConst, Bits: 32, Imm: 42}}, Result: 0}
	c, err := compileCode(p, TargetSpec{}, Func{ABI: abi.Go, Context: ContextGoManaged, AssumePointerFree: true, Stack: StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 640}})
	if c != nil {
		c.Close()
	}
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("managed bind with registration disabled: %v", err)
	}
	c, err = compileCode(p, TargetSpec{}, Func{ABI: abi.C})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if v, e := c.Call(); e != nil || v != 42 {
		t.Fatalf("non-managed execution: %d %v", v, e)
	}
}

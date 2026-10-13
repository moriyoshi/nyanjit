//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64le || loong64)

package compile

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/backend/goregister"
	"github.com/moriyoshi/nyanjit/builder"
)

func TestGeneratedGoCallCustomNameRejected(t *testing.T) {
	if !goregister.Supported() {
		t.Skip("registration disabled")
	}
	b := builder.NewBuilder()
	x, _ := b.Param()
	if err := b.Return(x); err != nil {
		t.Fatal(err)
	}
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	f := Func{ABI: abi.Go, Context: ContextGoManaged, AssumePointerFree: true, Stack: StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 240}}
	u, err := lowerUnit(p, TargetSpec{}, f)
	if err != nil {
		t.Fatal(err)
	}
	emission, err := u.encoded()
	if err != nil {
		t.Fatal(err)
	}
	argBytes := 0
	words := entryArgumentWords(emission.Report, 1)
	if emission.Report.Architecture == "386" {
		argBytes = words*4 + 8
	}
	info := emission.goUnwind
	leaf, err := execution.NewCode(emission.Bytes, execution.CodeOptions{ABI: abi.Go, GoManaged: true, Arity: 1, ArgumentWords: words, ParamBits: []uint8{64}}, func(code []byte) (bool, error) {
		return goregister.Register(code, goregister.Frame{Name: "ordinary.host.callback", CodeBytes: info.CodeBytes, FrameBytes: info.FrameBytes, ArgBytes: argBytes, SP: info.SP, PointerFree: true})
	})
	if err != nil {
		t.Fatal(err)
	}
	defer leaf.Close()
	entry, err := leaf.EntryAddress()
	if err != nil {
		t.Fatal(err)
	}
	b = builder.NewBuilder()
	x, _ = b.Param()
	fn, _ := b.ConstNative(entry)
	result, err := b.CallGo(fn, x)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(result); err != nil {
		t.Fatal(err)
	}
	p, err = b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	caller, err := compileCode(p, TargetSpec{}, f)
	if caller != nil {
		caller.Close()
	}
	if !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("generated target disguised by function name accepted: %v", err)
	}
}

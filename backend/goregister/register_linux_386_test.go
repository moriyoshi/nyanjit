//go:build !nyanjit_no_goregister && linux && 386

package goregister_test

import (
	"runtime"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/backend/goregister"
)

func TestRegister386PointerFreeLeaf(t *testing.T) {
	if !goregister.Supported() {
		t.Skip("runtime module layout is unsupported")
	}
	code, err := execution.Map([]byte{0xc3}) // ret
	if err != nil {
		t.Fatal(err)
	}
	linked, err := goregister.Register(code, goregister.Frame{
		Name: "goregister.test386", CodeBytes: len(code),
		SP: []goregister.SPPoint{{PC: 0, Delta: 0}}, PointerFree: true,
	})
	if err != nil || !linked {
		t.Fatalf("register 386 leaf: linked=%t err=%v", linked, err)
	}
	if fn := runtime.FuncForPC(execution.Entry(code)); fn == nil || fn.Name() != "goregister.test386" {
		t.Fatalf("registered function = %v", fn)
	}
}

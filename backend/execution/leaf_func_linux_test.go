//go:build linux && (amd64 || arm64 || 386 || riscv64 || loong64 || ppc64le)

package execution

import (
	"errors"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/moriyoshi/nyanjit/backend/abi"
)

func TestLeafFuncLeaseLifetime(t *testing.T) {
	c, err := NewCode(securityReturnBytes(), CodeOptions{ABI: abi.Go}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Keep only the lease alive after returning; the function value is gone.
	lease := func() *FuncLease {
		var f func() uint64
		l, err := c.LeafFunc(&f)
		if err != nil {
			t.Fatal(err)
		}
		if c.funcExposed {
			t.Fatal("leased entry permanently quarantined mapping")
		}
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
		runtime.GC()
		f()
		runtime.KeepAlive(l)
		return l
	}()
	c.mu.Lock()
	mapped := c.mem != nil
	c.mu.Unlock()
	if !mapped {
		t.Fatal("Close unmapped a leased entry")
	}
	runtime.KeepAlive(lease)
	lease = nil
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runtime.GC()
		c.mu.Lock()
		mapped = c.mem != nil
		c.mu.Unlock()
		if !mapped {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("mapping was not reclaimed after the lease became unreachable")
}

func TestLeafFuncErrorsPreserveDestination(t *testing.T) {
	original := func() uint64 { return 42 }
	f := original
	for _, opts := range []CodeOptions{{ABI: abi.C}, {ABI: abi.Go, Arity: 1, ParamBits: []uint8{64}}} {
		c, err := NewCode(securityReturnBytes(), opts, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.LeafFunc(&f)
		if err == nil {
			t.Fatal("accepted incompatible leaf entry")
		}
		if reflect.ValueOf(f).Pointer() != reflect.ValueOf(original).Pointer() {
			t.Fatal("changed destination on error")
		}
		if c.state.Load() != 0 {
			t.Fatal("failed export retained a lease")
		}
		c.Close()
		if c.mem != nil {
			t.Fatal("failed export retained executable mapping")
		}
		if _, err := c.LeafFunc(&f); !errors.Is(err, ErrClosed) {
			t.Fatal(err)
		}
	}
	var absent *Code
	if _, err := absent.LeafFunc(&f); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	c, err := NewCode(securityReturnBytes(), CodeOptions{ABI: abi.Go}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, bad := range []any{nil, 123, new(int), (*func())(nil)} {
		if _, err := c.LeafFunc(bad); !errors.Is(err, ErrSignature) {
			t.Fatalf("destination %T: %v", bad, err)
		}
	}
}

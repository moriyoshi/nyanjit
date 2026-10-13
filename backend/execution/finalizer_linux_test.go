//go:build linux && (amd64 || arm64 || 386 || riscv64 || loong64 || ppc64le)

package execution

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/goregister"
)

// countingFinalizers returns a one-element list and the number of times it
// has run.
func countingFinalizers() (backend.Finalizers, *atomic.Int32) {
	runs := new(atomic.Int32)
	return backend.Finalizers{backend.FinalizerFunc(func() { runs.Add(1) })}, runs
}

func TestFinalizersRunWhenLastCodeCloses(t *testing.T) {
	fs, runs := countingFinalizers()
	options := CodeOptions{ABI: abi.C, Finalizers: fs}
	// Two Templates from one program share the list, and so the count.
	first := NewTemplate(securityReturnBytes(), nil, nil, options, goregister.Frame{})
	second := NewTemplate(securityReturnBytes(), nil, nil, options, goregister.Frame{})
	a, err := first.Bind(nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := first.Bind(nil)
	if err != nil {
		t.Fatal(err)
	}
	c, err := second.Bind(nil)
	if err != nil {
		t.Fatal(err)
	}
	direct, err := NewCode(securityReturnBytes(), options, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i, code := range []*Code{a, b, c, direct} {
		if runs.Load() != 0 {
			t.Fatalf("finalizers ran after %d of 4 Closes", i)
		}
		if _, err := code.Call(); err != nil {
			t.Fatal(err)
		}
		if err := code.Close(); err != nil {
			t.Fatal(err)
		}
		if err := code.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if runs.Load() != 1 {
		t.Fatalf("finalizers ran %d times, want 1", runs.Load())
	}
	for _, template := range []*Template{first, second} {
		if _, err := template.Bind(nil); !errors.Is(err, ErrClosed) {
			t.Fatalf("Bind after finalizers: %v", err)
		}
	}
	if _, err := NewCode(securityReturnBytes(), options, nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("NewCode after finalizers: %v", err)
	}
	if _, err := first.Bytes(nil); err != nil {
		t.Fatalf("Bytes takes no share: %v", err)
	}
	if runs.Load() != 1 {
		t.Fatalf("finalizers ran %d times, want 1", runs.Load())
	}
}

func TestFinalizersSeparateLists(t *testing.T) {
	fs, runs := countingFinalizers()
	// A copy of the list is another list with its own count.
	other := append(backend.Finalizers(nil), fs...)
	a, err := NewCode(securityReturnBytes(), CodeOptions{ABI: abi.C, Finalizers: fs}, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewCode(securityReturnBytes(), CodeOptions{ABI: abi.C, Finalizers: other}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = a.Close()
	if runs.Load() != 1 {
		t.Fatalf("first list: %d runs", runs.Load())
	}
	_ = b.Close()
	if runs.Load() != 2 {
		t.Fatalf("second list: %d runs", runs.Load())
	}
}

func TestFinalizersWaitForCallInProgress(t *testing.T) {
	fs, runs := countingFinalizers()
	c, err := NewCode(securityReturnBytes(), CodeOptions{ABI: abi.C, Finalizers: fs}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Hold a call reference as an executing call does.
	if !c.beginCall() {
		t.Fatal("beginCall failed")
	}
	_ = c.Close()
	if runs.Load() != 0 {
		t.Fatal("finalizers ran while a call was in progress")
	}
	c.endCall()
	if runs.Load() != 1 {
		t.Fatalf("last call ended: %d runs", runs.Load())
	}
}

func TestFinalizersKeptByFuncExposure(t *testing.T) {
	fs, runs := countingFinalizers()
	c, err := NewCode(securityReturnBytes(), CodeOptions{ABI: abi.Go, Finalizers: fs}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var f func() uint64
	if err := c.Func(&f); err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	if runs.Load() != 0 {
		t.Fatal("finalizers ran although Func values remain callable")
	}
}

func TestFinalizersBindAll(t *testing.T) {
	fs, runs := countingFinalizers()
	options := CodeOptions{ABI: abi.C, Finalizers: fs}
	template := NewTemplate(securityReturnBytes(), nil, nil, options, goregister.Frame{})
	a, err := NewArena(64 << 10)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	codes, err := a.BindAll([]*Template{template, template}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// A failed BindAll gives its shares back without running the list.
	if _, err := a.BindAll([]*Template{template, nil}, nil); err == nil {
		t.Fatal("BindAll with a nil template succeeded")
	}
	_ = codes[0].Close()
	if runs.Load() != 0 {
		t.Fatal("finalizers ran with one Code open")
	}
	_ = codes[1].Close()
	if runs.Load() != 1 {
		t.Fatalf("%d runs after both Closes", runs.Load())
	}
	if _, err := a.BindAll([]*Template{template}, nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("BindAll after finalizers: %v", err)
	}
}

func TestFinalizersConcurrentClose(t *testing.T) {
	for round := 0; round < 16; round++ {
		fs, runs := countingFinalizers()
		template := NewTemplate(securityReturnBytes(), nil, nil, CodeOptions{ABI: abi.C, Finalizers: fs}, goregister.Frame{})
		codes := make([]*Code, 8)
		for i := range codes {
			var err error
			if codes[i], err = template.Bind(nil); err != nil {
				t.Fatal(err)
			}
		}
		start := make(chan struct{})
		var group sync.WaitGroup
		for _, c := range codes {
			group.Go(func() {
				<-start
				_, _ = c.Call()
				_ = c.Close()
			})
			group.Go(func() {
				<-start
				if code, err := template.Bind(nil); err == nil {
					_ = code.Close()
				} else if !errors.Is(err, ErrClosed) {
					t.Errorf("Bind: %v", err)
				}
			})
		}
		close(start)
		group.Wait()
		if runs.Load() != 1 {
			t.Fatalf("round %d: %d runs", round, runs.Load())
		}
	}
}

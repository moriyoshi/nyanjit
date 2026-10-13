package compile_test

import (
	"io"
	"reflect"
	"runtime"
	"runtime/trace"
	"sync"
	"testing"
	"time"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/compile/testdata/fpprobe"
)

//go:noinline
func securityFPCallee(a uint64) uint64 { return a ^ 0x5555 }

// securityFPGrow uses enough stack that a fresh goroutine's stack is copied.
//
//go:noinline
func securityFPGrow(depth int) uint64 {
	var pad [512]byte
	pad[depth%len(pad)] = byte(depth)
	if depth == 0 {
		return uint64(pad[0])
	}
	return securityFPGrow(depth-1) + uint64(pad[depth%len(pad)])
}

// securityFPStaleLinks counts saved frame pointers, from the caller's frame
// outward, that do not point above the record holding them. A stack copy
// that misses a frame record leaves one pointing into the old stack.
var securityFPStaleLinks int

//go:noinline
func securityFPGrowingCallee(a uint64) uint64 {
	sum := securityFPGrow(64)
	fp := fpprobe.FramePointer()
	for range 32 {
		next := fpprobe.Load(fp)
		if next == 0 {
			break
		}
		if next <= fp {
			securityFPStaleLinks++
			break
		}
		fp = next
	}
	return a ^ sum
}

func securityFPCode(t *testing.T, count int) *execution.Code {
	t.Helper()
	return securityFPCodeCalling(t, count, securityFPCallee)
}

func securityFPCodeCalling(t *testing.T, count int, callee func(uint64) uint64) *execution.Code {
	t.Helper()
	b := n.NewBuilder()
	x := b.Param()
	iv := x
	var live []n.Value
	for i := 0; i < count; i++ {
		iv = b.Add(b.Xor(iv, x), b.Const(uint64(i+9)))
		live = append(live, iv)
	}
	sum := b.CallGo(b.ConstNative(reflect.ValueOf(callee).Pointer()), iv)
	for _, v := range live {
		sum = b.Add(sum, v)
	}
	b.Return(sum)
	p, e := b.Finish()
	if e != nil {
		t.Fatal(e)
	}
	c, e := n.Compile(p, compile.TargetSpec{}, compile.Func{ABI: abi.C, Context: compile.ContextGoManaged, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 640}, AssumePointerFree: true})
	if managedUnsupported(e) {
		t.Skip("Go-managed code needs goregister:", e)
	}
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func TestSecuritySavedFramePointer(t *testing.T) {
	for count := 0; count <= 40; count++ {
		c := securityFPCode(t, count)
		addr, e := c.EntryAddress()
		if e != nil {
			t.Fatal(e)
		}
		before, _, after := fpprobe.Probe(addr, 3)
		c.Close()
		if before != after {
			t.Fatalf("shape %d overwrote caller FP: %#x -> %#x", count, before, after)
		}
	}
}

// Code.Call enters managed code at the trampoline's SP, so the frame record
// the assembler saved for it is the managed frame's reserved top word, which a
// stack copy adjusts (security audit 2026-10-25, F16).
func TestSecurityFramePointerChainAfterStackGrowth(t *testing.T) {
	for count := 0; count <= 12; count++ {
		c := securityFPCodeCalling(t, count, securityFPGrowingCallee)
		securityFPStaleLinks = 0
		for range 64 {
			done := make(chan error)
			go func() {
				_, e := c.Call(execution.Words(uint64(3))...)
				done <- e
			}()
			if e := <-done; e != nil {
				t.Fatal(e)
			}
		}
		c.Close()
		if securityFPStaleLinks != 0 {
			t.Fatalf("shape %d: %d calls left a stale frame pointer after stack growth", count, securityFPStaleLinks)
		}
	}
}

func TestSecurityFramePointerProfiling(t *testing.T) {
	c := securityFPCodeCalling(t, 3, securityFPGrowingCallee)
	defer c.Close()
	if e := trace.Start(io.Discard); e != nil {
		t.Fatal(e)
	}
	defer trace.Stop()
	old := runtime.SetMutexProfileFraction(1)
	defer runtime.SetMutexProfileFraction(old)
	runtime.SetBlockProfileRate(1)
	defer runtime.SetBlockProfileRate(0)
	var mu sync.Mutex
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(4)
	for i := 0; i < 4; i++ {
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					// A fresh goroutine's stack is copied while the callee
					// grows it, with the managed frame on the stack.
					done := make(chan error)
					go func() {
						_, e := c.Call(execution.Words(uint64(3))...)
						done <- e
					}()
					if e := <-done; e != nil {
						t.Error(e)
						return
					}
					mu.Lock()
					mu.Unlock()
				}
			}
		}()
	}
	for i := 0; i < 5; i++ {
		runtime.GC()
		time.Sleep(10 * time.Millisecond)
	}
	close(stop)
	wg.Wait()
}

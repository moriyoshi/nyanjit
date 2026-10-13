//go:build linux && (amd64 || arm64 || 386 || riscv64 || loong64 || ppc64le)

package execution

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
)

func TestCallRacesClose(t *testing.T) {
	for _, harden := range []bool{false, true} {
		t.Run(fmt.Sprint(harden), func(t *testing.T) {
			for round := 0; round < 32; round++ {
				c, err := NewCode(securityReturnBytes(), CodeOptions{ABI: abi.C, Harden: harden}, nil)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = c.Call(); err != nil {
					t.Fatal(err)
				}
				start := make(chan struct{})
				var group sync.WaitGroup
				for worker := 0; worker < 8; worker++ {
					group.Go(func() {
						<-start
						for call := 0; call < 64; call++ {
							var err error
							if call%2 == 0 {
								_, err = c.Call()
							} else {
								_, _, err = c.CallPair()
							}
							if err != nil && !errors.Is(err, ErrClosed) {
								t.Errorf("Call: %v", err)
								return
							}
						}
					})
				}
				for closer := 0; closer < 2; closer++ {
					group.Go(func() {
						<-start
						if err := c.Close(); err != nil {
							t.Errorf("Close: %v", err)
						}
					})
				}
				close(start)
				group.Wait()
				if c.mem != nil {
					t.Fatal("last call did not release mapping")
				}
				if _, err = c.EntryAddress(); !errors.Is(err, ErrClosed) {
					t.Fatalf("EntryAddress: %v", err)
				}
			}
		})
	}
}

func TestCloseWaitsForAcquiredCall(t *testing.T) {
	c, err := NewCode(securityReturnBytes(), CodeOptions{ABI: abi.C}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !c.beginCall() {
		t.Fatal("open code rejected")
	}
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
	if c.mem == nil {
		t.Fatal("Close unmapped an acquired call")
	}
	if c.beginCall() {
		t.Fatal("closed code admitted a call")
	}
	c.endCall()
	if c.mem != nil {
		t.Fatal("last call retained mapping")
	}
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestZeroCodeIsClosed(t *testing.T) {
	var c Code
	if _, err := c.Call(); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	if _, _, err := c.CallPair(); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	if _, err := c.EntryAddress(); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	var f func()
	if err := c.Func(&f); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
}

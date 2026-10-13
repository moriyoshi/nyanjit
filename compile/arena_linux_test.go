//go:build linux && (amd64 || arm64 || riscv64 || ppc64le || 386 || loong64)

package compile_test

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/backend/goregister"
	"github.com/moriyoshi/nyanjit/compile"
)

// addTemplate returns a template for x+k.
func addTemplate(t *testing.T, k uint64, fn compile.Func) *execution.Template {
	t.Helper()
	b := n.NewBuilder()
	b.Return(b.Add(b.Param(), b.Const(k)))
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	target, err := compile.NewTarget(compile.TargetSpec{})
	if err != nil {
		t.Fatal(err)
	}
	u, err := compile.Lower(target, p, fn)
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := u.Template()
	if err != nil {
		t.Fatal(err)
	}
	return tmpl
}

func entryOf(t *testing.T, code *execution.Code) uintptr {
	t.Helper()
	entry, err := code.EntryAddress()
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

func callOne(t *testing.T, code *execution.Code, x uint64) uint64 {
	t.Helper()
	lo, hi, err := code.CallPair(execution.Words(x)...)
	if err != nil {
		t.Fatal(err)
	}
	return execution.Wide(lo, hi)
}

// mappingPerms returns the /proc/self/maps permissions of the mapping
// containing addr, or "" if none does.
func mappingPerms(t *testing.T, addr uintptr) string {
	t.Helper()
	f, err := os.Open("/proc/self/maps")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	lines := bufio.NewScanner(f)
	for lines.Scan() {
		var start, end uintptr
		var perms string
		if _, err := fmt.Sscanf(lines.Text(), "%x-%x %s", &start, &end, &perms); err == nil && start <= addr && addr < end {
			return perms
		}
	}
	return ""
}

func TestArenaReusesPages(t *testing.T) {
	page := uintptr(os.Getpagesize())
	tmpl := addTemplate(t, 1, compile.Func{ABI: abi.C})
	arena, err := execution.NewArena(2 * int(page))
	if err != nil {
		t.Fatal(err)
	}
	defer arena.Close()
	first, err := tmpl.BindIn(arena, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := tmpl.BindIn(arena, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	a, b := entryOf(t, first), entryOf(t, second)
	if b-a != page {
		t.Fatalf("bindings at %#x and %#x, want one page apart", a, b)
	}
	if got := mappingPerms(t, a); got != "r-xp" {
		t.Fatalf("bound page has permissions %q", got)
	}
	if _, err := tmpl.BindIn(arena, nil); !errors.Is(err, execution.ErrArenaFull) {
		t.Fatalf("third binding: got %v, want ErrArenaFull", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if got := mappingPerms(t, a); got != "---p" {
		t.Fatalf("released page has permissions %q", got)
	}
	// The released page is reused, and the new code runs rather than the old.
	other := addTemplate(t, 100, compile.Func{ABI: abi.C})
	third, err := other.BindIn(arena, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer third.Close()
	if entryOf(t, third) != a {
		t.Fatalf("reused binding at %#x, want %#x", entryOf(t, third), a)
	}
	if got := callOne(t, third, 5); got != 105 {
		t.Fatalf("reused page returned %d, want 105", got)
	}
	if got := callOne(t, second, 5); got != 6 {
		t.Fatalf("neighbour returned %d, want 6", got)
	}
}

func TestArenaCloseWaitsForBoundCode(t *testing.T) {
	// A hardened entry sits up to 4 KiB into its pages.
	tmpl := addTemplate(t, 2, compile.Func{ABI: abi.C, Harden: true})
	arena, err := execution.NewArena(2 * os.Getpagesize())
	if err != nil {
		t.Fatal(err)
	}
	code, err := tmpl.BindIn(arena, nil)
	if err != nil {
		t.Fatal(err)
	}
	entry := entryOf(t, code)
	if err := arena.Close(); err != nil {
		t.Fatal(err)
	}
	if err := arena.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := tmpl.BindIn(arena, nil); !errors.Is(err, execution.ErrClosed) {
		t.Fatalf("binding in a closed arena: got %v, want ErrClosed", err)
	}
	if got := callOne(t, code, 40); got != 42 {
		t.Fatalf("got %d, want 42", got)
	}
	if err := code.Close(); err != nil {
		t.Fatal(err)
	}
	if got := mappingPerms(t, entry); got != "" {
		t.Fatalf("arena range still mapped (%s) after its last code closed", got)
	}
}

func TestArenaConcurrentBindings(t *testing.T) {
	tmpl := addTemplate(t, 3, compile.Func{ABI: abi.C})
	arena, err := execution.NewArena(8 * os.Getpagesize())
	if err != nil {
		t.Fatal(err)
	}
	defer arena.Close()
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for g := range 16 {
		wg.Go(func() {
			for i := range 50 {
				code, err := tmpl.BindIn(arena, nil)
				if errors.Is(err, execution.ErrArenaFull) {
					continue
				}
				if err != nil {
					errs <- err
					return
				}
				x := uint64(g*1000 + i)
				lo, hi, err := code.CallPair(execution.Words(x)...)
				if err == nil && execution.Wide(lo, hi) != x+3 {
					err = fmt.Errorf("got %d, want %d", execution.Wide(lo, hi), x+3)
				}
				if cerr := code.Close(); err == nil {
					err = cerr
				}
				if err != nil {
					errs <- err
					return
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	// Every page came back: the whole arena fits one binding per page again.
	var codes []*execution.Code
	for range 8 {
		code, err := tmpl.BindIn(arena, nil)
		if err != nil {
			t.Fatal(err)
		}
		codes = append(codes, code)
	}
	for _, code := range codes {
		code.Close()
	}
}

func TestArenaKeepsManagedPages(t *testing.T) {
	if !goregister.Supported() {
		t.Skip("Go-managed entries need goregister")
	}
	managed := compile.Func{ABI: abi.Go, Context: compile.ContextGoManaged, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}, AssumePointerFree: true}
	tmpl := addTemplate(t, 4, managed)
	arena, err := execution.NewArena(1)
	if err != nil {
		t.Fatal(err)
	}
	defer arena.Close()
	code, err := tmpl.BindIn(arena, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := callOne(t, code, 1); got != 5 {
		t.Fatalf("got %d, want 5", got)
	}
	code.Close()
	// The runtime keeps the registered text, so its page is never reused.
	if _, err := tmpl.BindIn(arena, nil); !errors.Is(err, execution.ErrArenaFull) {
		t.Fatalf("binding after closing managed code: got %v, want ErrArenaFull", err)
	}
}

func TestArenaRejectsBadSize(t *testing.T) {
	for _, size := range []int{0, -1, int(^uint(0) >> 1)} {
		if _, err := execution.NewArena(size); !errors.Is(err, execution.ErrInvalidCodeOptions) {
			t.Errorf("NewArena(%d): got %v, want ErrInvalidCodeOptions", size, err)
		}
	}
	// A nil arena is Bind.
	code, err := addTemplate(t, 5, compile.Func{ABI: abi.C}).BindIn(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	if got := callOne(t, code, 1); got != 6 {
		t.Fatalf("got %d, want 6", got)
	}
}

//go:build linux && (amd64 || arm64 || 386 || riscv64 || loong64 || ppc64le)

package execution

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/goregister"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

// returnBytes returns native code that executes nops no-ops and returns k.
func returnBytes(k uint16, nops int) []byte {
	var nop []byte
	var body []byte
	word := func(w uint32) []byte { return binary.LittleEndian.AppendUint32(nil, w) }
	switch runtime.GOARCH {
	case "amd64", "386":
		nop = []byte{0x90}
		body = append([]byte{0xb8}, binary.LittleEndian.AppendUint32(nil, uint32(k))...)
		body = append(body, 0xc3)
	case "arm64":
		nop = word(0xd503201f)
		body = append(word(0xd2800000|uint32(k)<<5), word(0xd65f03c0)...)
	case "riscv64":
		nop = word(0x00000013)
		body = append(word(uint32(k&0x7ff)<<20|10<<7|0x13), word(0x00008067)...)
	case "loong64":
		nop = word(0x03400000)
		body = append(word(0x03800000|uint32(k&0xfff)<<10|4), word(0x4c000020)...)
	case "ppc64le":
		nop = word(0x60000000)
		body = append(word(0x38600000|uint32(k&0x7fff)), word(0x4e800020)...)
	default:
		panic("unsupported test architecture")
	}
	return append(slices.Repeat(nop, nops), body...)
}

func returnTemplate(k uint16, nops int, harden bool) *Template {
	return NewTemplate(returnBytes(k, nops), nil, nil, CodeOptions{ABI: abi.C, Harden: harden}, goregister.Frame{})
}

// batchTemplates returns templates of mixed sizes, some spanning pages.
func batchTemplates(n int) []*Template {
	templates := make([]*Template, n)
	for i := range templates {
		nops := 0
		switch i % 5 {
		case 1:
			nops = 300
		case 3:
			nops = 3000 // over a page on 4 KiB-page hosts
		}
		templates[i] = returnTemplate(uint16(100+i), nops, i%4 == 2)
	}
	return templates
}

type mapsRange struct {
	lo, hi uint64
	perms  string
}

func readMaps(t testing.TB) []mapsRange {
	t.Helper()
	data, err := os.ReadFile("/proc/self/maps")
	if err != nil {
		t.Fatal(err)
	}
	var ranges []mapsRange
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		lo, hi, ok := strings.Cut(f[0], "-")
		if !ok {
			continue
		}
		a, e1 := strconv.ParseUint(lo, 16, 64)
		b, e2 := strconv.ParseUint(hi, 16, 64)
		if e1 == nil && e2 == nil {
			ranges = append(ranges, mapsRange{a, b, f[1]})
		}
	}
	return ranges
}

// pagePermissions returns the permissions of the page holding address.
func pagePermissions(t testing.TB, address uintptr) string {
	t.Helper()
	for _, r := range readMaps(t) {
		if uint64(address) >= r.lo && uint64(address) < r.hi {
			return r.perms
		}
	}
	t.Fatalf("mapping %#x absent", address)
	return ""
}

// writableExecutable returns any mapping overlapping mem that is both
// writable and executable.
func writableExecutable(t testing.TB, mem []byte) []mapsRange {
	t.Helper()
	lo, hi := uint64(Entry(mem)), uint64(Entry(mem))+uint64(len(mem))
	var bad []mapsRange
	for _, r := range readMaps(t) {
		if r.hi > lo && r.lo < hi && len(r.perms) >= 3 && r.perms[1] == 'w' && r.perms[2] == 'x' {
			bad = append(bad, r)
		}
	}
	return bad
}

func checkCalls(t *testing.T, codes []*Code, closed []bool) {
	t.Helper()
	for i, c := range codes {
		got, err := c.Call()
		switch {
		case closed != nil && closed[i]:
			if !errors.Is(err, ErrClosed) {
				t.Fatalf("closed code %d: %v", i, err)
			}
		case err != nil:
			t.Fatalf("code %d: %v", i, err)
		case got != uintptr(100+i):
			t.Fatalf("code %d returned %d", i, got)
		}
	}
}

func checkArenaEmpty(t *testing.T, a *Arena) {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.used != 0 || len(a.free) != 1 || a.free[0] != (pageRun{0, len(a.mem) / a.page}) {
		t.Fatalf("arena not empty: used %d, free %v", a.used, a.free)
	}
}

func TestBindAllCallsEachCode(t *testing.T) {
	a, err := NewArena(64 << 10)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	templates := batchTemplates(10)
	codes, err := a.BindAll(templates, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != len(templates) {
		t.Fatalf("%d codes for %d templates", len(codes), len(templates))
	}
	checkCalls(t, codes, nil)
	// The images are packed into one run in order.
	run := codes[0].shared
	end := 0
	for i, c := range codes {
		if c.shared != run || c.arena != a {
			t.Fatalf("code %d is not in the batch run", i)
		}
		offset := int(Entry(c.mem) - Entry(run.pages))
		if offset%slotAlignment != 0 || offset < end || offset-end >= slotAlignment {
			t.Fatalf("code %d at offset %d after %d", i, offset, end)
		}
		end = offset + len(c.mem)
		if c.options.Harden != (c.entry != Entry(c.mem)) {
			t.Fatalf("code %d entry offset %d with Harden %v", i, c.entry-Entry(c.mem), c.options.Harden)
		}
	}
	if want := (end + a.page - 1) / a.page * a.page; len(run.pages) != want {
		t.Fatalf("run spans %d bytes, want %d", len(run.pages), want)
	}
	if p := pagePermissions(t, Entry(run.pages)); !strings.HasPrefix(p, "r-x") {
		t.Fatalf("batch permissions %s", p)
	}
	if bad := writableExecutable(t, a.mem); bad != nil {
		t.Fatalf("writable and executable: %v", bad)
	}
	for _, c := range codes {
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}
	checkArenaEmpty(t, a)
}

func TestBindAllCloseOrders(t *testing.T) {
	const n = 12
	orders := map[string][]int{}
	forward := make([]int, n)
	for i := range forward {
		forward[i] = i
	}
	orders["forward"] = forward
	orders["reverse"] = slices.Concat(forward)
	slices.Reverse(orders["reverse"])
	var evensFirst []int
	for i := 0; i < n; i += 2 {
		evensFirst = append(evensFirst, i)
	}
	for i := 1; i < n; i += 2 {
		evensFirst = append(evensFirst, i)
	}
	orders["interleaved"] = evensFirst
	random := slices.Concat(forward)
	rand.New(rand.NewPCG(1, 2)).Shuffle(n, func(i, j int) { random[i], random[j] = random[j], random[i] })
	orders["random"] = random
	for name, order := range orders {
		t.Run(name, func(t *testing.T) {
			a, err := NewArena(64 << 10)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			codes, err := a.BindAll(batchTemplates(n), nil)
			if err != nil {
				t.Fatal(err)
			}
			run := codes[0].shared
			closed := make([]bool, n)
			for _, i := range order {
				mem := codes[i].mem
				if err := codes[i].Close(); err != nil {
					t.Fatal(err)
				}
				if err := codes[i].Close(); err != nil {
					t.Fatalf("second Close: %v", err)
				}
				closed[i] = true
				checkCalls(t, codes, closed)
				// Each page the closed code occupied is inaccessible exactly
				// when no open code remains on it.
				first, last := run.span(mem, a.page)
				for p := first; p <= last; p++ {
					open := false
					for j, c := range codes {
						if closed[j] {
							continue
						}
						f, l := run.span(c.mem, a.page)
						open = open || f <= p && p <= l
					}
					perms := pagePermissions(t, Entry(run.pages[p*a.page:]))
					if open != strings.HasPrefix(perms, "r-x") || !open && !strings.HasPrefix(perms, "---") {
						t.Fatalf("page %d with open=%v has permissions %s", p, open, perms)
					}
				}
			}
			checkArenaEmpty(t, a)
			// The whole range is reusable.
			if _, err := a.place(make([]byte, len(a.mem)), nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBindAllClosedArenaReleasesAfterLastCode(t *testing.T) {
	a, err := NewArena(64 << 10)
	if err != nil {
		t.Fatal(err)
	}
	codes, err := a.BindAll(batchTemplates(4), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.BindAll(batchTemplates(1), nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("BindAll after Close: %v", err)
	}
	if _, err := a.BindAll(nil, nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("empty BindAll after Close: %v", err)
	}
	checkCalls(t, codes, nil)
	for i, c := range codes {
		if a.mem == nil {
			t.Fatalf("range released with %d codes open", len(codes)-i)
		}
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if a.mem != nil {
		t.Fatal("range kept after the last code closed")
	}
}

func TestBindAllEmptyAndNil(t *testing.T) {
	var none *Arena
	if _, err := none.BindAll(batchTemplates(1), nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("nil arena: %v", err)
	}
	a, err := NewArena(4096)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	codes, err := a.BindAll(nil, nil)
	if err != nil || codes == nil || len(codes) != 0 {
		t.Fatalf("empty BindAll = %v, %v", codes, err)
	}
	checkArenaEmpty(t, a)
}

func TestBindAllErrorsLeaveArenaUnchanged(t *testing.T) {
	a, err := NewArena(4 * pageSize())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	// Occupy the first page so the free run does not start the range.
	held, err := returnTemplate(7, 0, false).BindIn(a, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	unbound := NewTemplate(make([]byte, 16), nil, []machine.Relocation{{Symbol: "missing", Encoding: machine.RelocAMD64Immediate}}, CodeOptions{ABI: abi.C}, goregister.Frame{})
	external := NewTemplate(returnBytes(1, 0), nil, nil, CodeOptions{ABI: abi.C, External: true}, goregister.Frame{})
	invalid := NewTemplate(returnBytes(1, 0), nil, nil, CodeOptions{ABI: abi.C, Arity: -1}, goregister.Frame{})
	page := a.page
	big := returnTemplate(1, page, false) // over a page of nops
	cases := []struct {
		name      string
		templates []*Template
		bindings  Symbols
		want      error
	}{
		{"full", []*Template{big, big, big}, nil, ErrArenaFull},
		{"unbound", []*Template{returnTemplate(1, 0, false), unbound}, Symbols{"other": 1}, machine.ErrUnboundSymbol},
		{"unused", []*Template{returnTemplate(1, 0, false)}, Symbols{"other": 1}, backend.ErrInvalidFunction},
		{"nil", []*Template{returnTemplate(1, 0, false), nil}, nil, backend.ErrInvalidFunction},
		{"external", []*Template{external}, nil, ErrExternalCallOnly},
		{"options", []*Template{returnTemplate(1, 0, false), invalid}, nil, ErrInvalidCodeOptions},
	}
	for _, tc := range cases {
		a.mu.Lock()
		free, used := slices.Clone(a.free), a.used
		a.mu.Unlock()
		codes, err := a.BindAll(tc.templates, tc.bindings)
		if !errors.Is(err, tc.want) || codes != nil {
			t.Fatalf("%s: BindAll = %v, %v; want %v", tc.name, codes, err, tc.want)
		}
		a.mu.Lock()
		if !slices.Equal(a.free, free) || a.used != used {
			t.Fatalf("%s: arena changed: free %v used %d, was %v %d", tc.name, a.free, a.used, free, used)
		}
		a.mu.Unlock()
		for _, run := range free {
			if p := pagePermissions(t, Entry(a.mem[run.start*page:])); !strings.HasPrefix(p, "---") {
				t.Fatalf("%s: free page has permissions %s", tc.name, p)
			}
		}
	}
	// A failing relocation is reported after the pages were taken.
	failed := errors.New("relocation failed")
	m, err := returnTemplate(1, 0, false).prepare(nil)
	if err != nil {
		t.Fatal(err)
	}
	bad := m
	bad.relocate = func([]byte) error { return failed }
	a.mu.Lock()
	free := slices.Clone(a.free)
	a.mu.Unlock()
	if codes, err := a.bindMappings([]mapping{m, bad, m}); !errors.Is(err, failed) || codes != nil {
		t.Fatalf("relocation failure: %v, %v", codes, err)
	}
	a.mu.Lock()
	if !slices.Equal(a.free, free) || a.used != 1 {
		t.Fatalf("relocation failure changed the arena: free %v used %d", a.free, a.used)
	}
	a.mu.Unlock()
	// The arena still binds a batch that fits.
	codes, err := a.BindAll([]*Template{returnTemplate(100, 0, false), returnTemplate(101, 0, true)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	checkCalls(t, codes, nil)
	for _, c := range codes {
		c.Close()
	}
}

// TestBindAllRegistrationFailure checks that a registration failure returns
// every page except those of code the runtime retained.
func TestBindAllRegistrationFailure(t *testing.T) {
	a, err := NewArena(16 * pageSize())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	plain, err := returnTemplate(1, 0, false).prepare(nil)
	if err != nil {
		t.Fatal(err)
	}
	// A page of nops keeps the retained code off the pages of the rest.
	wide, err := returnTemplate(1, a.page, false).prepare(nil)
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("rejected")
	managed := func(m mapping, linked bool, err error) mapping {
		m.register = func([]byte) (bool, error) { return linked, err }
		m.options.GoManaged = true
		return m
	}
	// Rejected before anything was retained: the arena is unchanged.
	if codes, err := a.bindMappings([]mapping{plain, managed(plain, false, failure), plain}); !errors.Is(err, failure) || codes != nil {
		t.Fatalf("unretained failure: %v, %v", codes, err)
	}
	checkArenaEmpty(t, a)
	if codes, err := a.bindMappings([]mapping{managed(plain, false, nil)}); !errors.Is(err, ErrInvalidCodeOptions) || codes != nil {
		t.Fatalf("unlinked registration: %v, %v", codes, err)
	}
	checkArenaEmpty(t, a)
	// goregister errors map to the package's.
	if _, err := a.bindMappings([]mapping{managed(plain, false, goregister.ErrUnsupported)}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unsupported registration: %v", err)
	}
	checkArenaEmpty(t, a)
	// Retained, then a failure: only the retained code's pages stay.
	codes, err := a.bindMappings([]mapping{plain, managed(wide, true, nil), wide, managed(plain, false, failure), plain})
	if !errors.Is(err, failure) || codes != nil {
		t.Fatalf("retained failure: %v, %v", codes, err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.used != 1 {
		t.Fatalf("used %d after retained failure, want 1", a.used)
	}
	taken := 0
	for _, run := range a.free {
		taken += run.pages
	}
	// The retained code follows plain's slot; the pages it spans stay.
	want := (slotAlignment+len(wide.image)-1)/a.page + 1
	if taken = len(a.mem)/a.page - taken; taken != want {
		t.Fatalf("%d pages kept for one retained code, want %d", taken, want)
	}
}

func TestBindAllLinksEachSlot(t *testing.T) {
	a, err := NewArena(64 << 10)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	// Not executed: these patches are architecture-independent fixtures,
	// except that code-base literals need a 32-bit address space.
	body := make([]byte, 48)
	copy(body, []byte{0xf2, 0x0f, 0x10, 0x1d, 0, 0, 0, 0})
	literal := NewTemplate(body, nil, []machine.Relocation{{Encoding: machine.Reloc386Literal, TargetOffset: 32}}, CodeOptions{ABI: abi.C, Harden: true}, goregister.Frame{})
	immediate := func(symbol string) *Template {
		return NewTemplate([]byte{0x48, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0}, nil, []machine.Relocation{{Symbol: symbol, Encoding: machine.RelocAMD64Immediate}}, CodeOptions{ABI: abi.C}, goregister.Frame{})
	}
	templates := []*Template{immediate("a"), immediate("b"), immediate("a")}
	if runtime.GOARCH == "386" {
		templates = []*Template{literal, immediate("a"), literal, immediate("b"), literal}
	}
	values := Symbols{"a": 0x123456789abcdef0, "b": 0x0fedcba987654321}
	codes, err := a.BindAll(templates, values)
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range codes {
		offset := int(c.entry - Entry(c.mem))
		if relocation := templates[i].relocations[0]; relocation.NeedsCodeBase() {
			if got := binary.LittleEndian.Uint32(c.mem[offset+4:]); got != uint32(c.entry+32) {
				t.Fatalf("code %d literal %#x, entry %#x", i, got, c.entry)
			}
		} else if got := binary.LittleEndian.Uint64(c.mem[offset+2:]); got != values[relocation.Symbol] {
			t.Fatalf("code %d immediate %#x", i, got)
		}
		c.Close()
	}
	checkArenaEmpty(t, a)
}

// TestBindAllConcurrent binds, calls and closes batches from several
// goroutines while another watches that no arena page is ever writable and
// executable at once.
func TestBindAllConcurrent(t *testing.T) {
	a, err := NewArena(1 << 20)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	var stop atomic.Bool
	var watcher sync.WaitGroup
	var scans atomic.Int64
	watcher.Go(func() {
		for !stop.Load() {
			if bad := writableExecutable(t, a.mem); bad != nil {
				t.Errorf("writable and executable: %v", bad)
				return
			}
			scans.Add(1)
		}
	})
	rounds := 40
	if testing.Short() {
		rounds = 10
	}
	var group sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		group.Go(func() {
			random := rand.New(rand.NewPCG(uint64(worker), 7))
			for round := 0; round < rounds && !t.Failed(); round++ {
				n := 1 + random.IntN(16)
				codes, err := a.BindAll(batchTemplates(n), nil)
				if err != nil {
					t.Errorf("BindAll: %v", err)
					return
				}
				// Close in random order while other goroutines call the rest.
				order := random.Perm(n)
				var callers sync.WaitGroup
				for _, c := range codes {
					callers.Go(func() {
						for range 16 {
							if _, err := c.Call(); err != nil && !errors.Is(err, ErrClosed) {
								t.Errorf("Call: %v", err)
							}
						}
					})
				}
				for _, i := range order {
					if err := codes[i].Close(); err != nil {
						t.Errorf("Close: %v", err)
					}
				}
				callers.Wait()
			}
		})
	}
	group.Wait()
	stop.Store(true)
	watcher.Wait()
	if scans.Load() == 0 {
		t.Fatal("watcher never scanned")
	}
	checkArenaEmpty(t, a)
}

func benchmarkTemplates(n int) []*Template {
	templates := make([]*Template, n)
	for i := range templates {
		templates[i] = returnTemplate(uint16(i), 16, false)
	}
	return templates
}

func BenchmarkBindClose(b *testing.B) {
	for _, n := range []int{1, 8, 64} {
		templates := benchmarkTemplates(n)
		b.Run(fmt.Sprintf("Bind/%d", n), func(b *testing.B) {
			codes := make([]*Code, n)
			for b.Loop() {
				for i, t := range templates {
					var err error
					if codes[i], err = t.Bind(nil); err != nil {
						b.Fatal(err)
					}
				}
				for _, c := range codes {
					c.Close()
				}
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*n)/1e3, "µs/func")
		})
		b.Run(fmt.Sprintf("BindIn/%d", n), func(b *testing.B) {
			a, err := NewArena(1 << 20)
			if err != nil {
				b.Fatal(err)
			}
			defer a.Close()
			codes := make([]*Code, n)
			for b.Loop() {
				for i, t := range templates {
					if codes[i], err = t.BindIn(a, nil); err != nil {
						b.Fatal(err)
					}
				}
				for _, c := range codes {
					c.Close()
				}
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*n)/1e3, "µs/func")
		})
		b.Run(fmt.Sprintf("BindAll/%d", n), func(b *testing.B) {
			a, err := NewArena(1 << 20)
			if err != nil {
				b.Fatal(err)
			}
			defer a.Close()
			for b.Loop() {
				codes, err := a.BindAll(templates, nil)
				if err != nil {
					b.Fatal(err)
				}
				for _, c := range codes {
					c.Close()
				}
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*n)/1e3, "µs/func")
		})
	}
}

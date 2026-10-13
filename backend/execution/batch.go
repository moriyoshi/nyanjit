package execution

import (
	"fmt"
	"math"
	"unsafe"

	"github.com/moriyoshi/nyanjit/backend"
)

// slotAlignment separates the images that one BindAll packs into shared
// pages. It keeps the 16-byte instruction and literal alignment that every
// image assumes, and gives each function its own cache lines.
const slotAlignment = 64

// sharedRun is a run of pages that one BindAll filled for several Codes.
type sharedRun struct {
	pages []byte
	refs  []int32 // open Codes on each page; guarded by Arena.mu
}

// BindAll binds templates into consecutive slots of one free run of pages
// with a single write-to-execute transition for all of them, and returns
// their Codes in order. Each template is linked as Bind would link it with
// the bindings its relocations and Go call targets name; a binding that no
// template names returns backend.ErrInvalidFunction. Otherwise BindAll
// returns the errors of BindIn, where ErrArenaFull means that no free run
// fits all of the templates. On error no Code is returned and the arena is
// unchanged, with one exception: when registering a Go-managed template fails
// after the Go runtime retained code from the batch, the pages of that code
// stay reserved, as they would after a failed BindIn.
//
// Each Code is closed independently. Neighbouring Codes share the pages at
// their slot boundaries, and a page is made inaccessible and returned to the
// arena when the last Code on it is closed, so closing a Code whose pages all
// remain shared makes no system call. Until then the closed Code's bytes stay
// mapped read-execute: no call can enter them through the closed Code, and
// they are not overwritten, since that would make a page writable while other
// code on it may run. No page is ever writable and executable at once.
//
// An empty templates binds nothing and returns no Codes. A nil Arena returns
// ErrClosed.
func (a *Arena) BindAll(templates []*Template, bindings Symbols) ([]*Code, error) {
	if a == nil {
		return nil, ErrClosed
	}
	if len(templates) == 0 {
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.closed || a.mem == nil {
			return nil, ErrClosed
		}
		return []*Code{}, nil
	}
	mappings := make([]mapping, len(templates))
	var used map[string]bool
	if len(bindings) != 0 {
		used = make(map[string]bool, len(bindings))
	}
	for i, t := range templates {
		var err error
		if mappings[i], err = t.prepare(t.symbolsFrom(bindings, used)); err != nil {
			return nil, err
		}
	}
	if len(used) != len(bindings) {
		return nil, backend.ErrInvalidFunction
	}
	return a.bindMappings(mappings)
}

// symbolsFrom returns the bindings that t's relocations and Go call targets
// name, and marks those names in used.
func (t *Template) symbolsFrom(bindings Symbols, used map[string]bool) Symbols {
	if t == nil || len(bindings) == 0 {
		return nil
	}
	var symbols Symbols
	pick := func(name string) {
		if value, ok := bindings[name]; ok {
			if symbols == nil {
				symbols = make(Symbols, len(bindings))
			}
			symbols[name] = value
			used[name] = true
		}
	}
	for _, r := range t.relocations {
		if !r.NeedsCodeBase() {
			pick(r.Symbol)
		}
	}
	for _, target := range t.options.GoCallTargets {
		if target.Symbol != "" {
			pick(target.Symbol)
		}
	}
	return symbols
}

// bindMappings places prepared images as BindAll does.
func (a *Arena) bindMappings(mappings []mapping) ([]*Code, error) {
	offsets := make([]int, len(mappings))
	size := 0
	for i := range mappings {
		size = (size + slotAlignment - 1) &^ (slotAlignment - 1)
		if len(mappings[i].image) > math.MaxInt/2-size {
			return nil, fmt.Errorf("%d templates: %w", len(mappings), ErrArenaFull)
		}
		offsets[i] = size
		size += len(mappings[i].image)
	}
	pages, err := a.take(size, len(mappings))
	if err != nil {
		return nil, err
	}
	if err = a.fillRun(pages, size, mappings, offsets); err != nil {
		_ = a.releasePages(pages, len(mappings))
		return nil, err
	}
	run := &sharedRun{pages: pages, refs: make([]int32, len(pages)/a.page)}
	codes := make([]*Code, len(mappings))
	for i := range mappings {
		slot := pages[offsets[i] : offsets[i]+len(mappings[i].image) : offsets[i]+len(mappings[i].image)]
		first, last := run.span(slot, a.page)
		for p := first; p <= last; p++ {
			run.refs[p]++
		}
		codes[i] = mappings[i].code(slot, a, run)
	}
	if err = a.registerRun(run, mappings, codes); err != nil {
		return nil, err
	}
	return codes, nil
}

// fillRun makes the fresh pages writable, writes and relocates every image
// at its offset, and seals the pages executable. size is the number of bytes
// written.
func (a *Arena) fillRun(pages []byte, size int, mappings []mapping, offsets []int) error {
	if err := commitPages(pages); err != nil {
		return err
	}
	for i := range mappings {
		end := offsets[i] + len(mappings[i].image)
		if err := writeCode(pages[offsets[i]:end:end], mappings[i].image, mappings[i].relocate); err != nil {
			return err
		}
	}
	return makeExecutable(pages, size)
}

// registerRun registers the Go-managed Codes of a filled run. On failure it
// returns the run's pages to the arena, except those holding code the Go
// runtime already retained.
func (a *Arena) registerRun(run *sharedRun, mappings []mapping, codes []*Code) error {
	var retained []bool
	var failure error
	for i := range mappings {
		if mappings[i].register == nil {
			continue
		}
		linked, err := mappings[i].register(mappings[i].body(codes[i].mem))
		if linked {
			if retained == nil {
				retained = make([]bool, len(codes))
			}
			retained[i] = true
		}
		if err == nil && !linked {
			err = ErrInvalidCodeOptions
		}
		if err != nil {
			failure = bindError(err)
			break
		}
	}
	switch {
	case failure == nil:
		return nil
	case retained == nil:
		_ = a.releasePages(run.pages, len(codes))
	default:
		for i, c := range codes {
			if !retained[i] {
				_ = a.releaseShared(run, c.mem)
			}
		}
	}
	return failure
}

// span returns the indices of the first and last pages slot occupies.
func (r *sharedRun) span(slot []byte, page int) (first, last int) {
	offset := int(uintptr(unsafe.Pointer(&slot[0])) - uintptr(unsafe.Pointer(&r.pages[0])))
	return offset / page, (offset + len(slot) - 1) / page
}

// releaseShared drops a closed Code's hold on the pages of run that mem
// occupies, and returns the pages no other Code holds to the arena.
func (a *Arena) releaseShared(run *sharedRun, mem []byte) error {
	first, last := run.span(mem, a.page)
	lo, hi := 0, 0
	a.mu.Lock()
	for p := first; p <= last; p++ {
		run.refs[p]--
		if run.refs[p] == 0 {
			// Pages strictly inside mem hold no other Code, so the free
			// pages form one run.
			if hi == 0 {
				lo = p
			}
			hi = p + 1
		}
	}
	if hi == 0 {
		a.used--
		err := a.releaseRangeLocked()
		a.mu.Unlock()
		return err
	}
	a.mu.Unlock()
	return a.release(run.pages[lo*a.page : hi*a.page : hi*a.page])
}

// writeCode copies code into the writable mem and relocates it in place.
func writeCode(mem, code []byte, relocate func([]byte) error) error {
	copy(mem, code)
	if relocate != nil {
		return relocate(mem)
	}
	return nil
}

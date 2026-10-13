package execution

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"unsafe"
)

// ErrArenaFull means an Arena has no free run of pages large enough for a
// binding.
var ErrArenaFull = errors.New("execution: arena is full")

// Arena is a range of address space reserved for executable code. Each
// Template.BindIn takes whole pages from the arena instead of creating a
// mapping of its own, so functions bound in one arena share one address
// range. Arena.BindAll binds several templates into one run of pages with a
// single write-to-execute transition. Free pages are inaccessible and commit
// no memory. Closing a Code returns its pages to the arena.
//
// An Arena is safe for concurrent use. Close stops new bindings; the range
// itself is released once every Code bound in it has been closed and its
// calls have returned. Go-managed code and code exposed through Func are never
// released (see Code.Close), so their pages, and with them the arena's range,
// stay reserved for the process lifetime.
type Arena struct {
	mu     sync.Mutex
	mem    []byte    // the whole reservation; nil once released
	page   int       // page size in bytes
	free   []pageRun // free runs, sorted by start and coalesced
	used   int       // Codes holding pages
	closed bool
}

type pageRun struct{ start, pages int }

// NewArena reserves size bytes, rounded up to whole pages, for executable
// code. It returns ErrInvalidCodeOptions for a size that is not positive and
// ErrUnsupported where native execution is unsupported.
func NewArena(size int) (*Arena, error) {
	page := pageSize()
	if size <= 0 || size > int(^uint(0)>>1)-page {
		return nil, fmt.Errorf("arena of %d bytes: %w", size, ErrInvalidCodeOptions)
	}
	pages := (size + page - 1) / page
	mem, err := reserveArena(pages * page)
	if err != nil {
		return nil, err
	}
	return &Arena{mem: mem, page: page, free: []pageRun{{0, pages}}}, nil
}

// Close stops new bindings and is safe to call more than once. Code already
// bound keeps running; the arena's range is released after the last of it is
// closed. Close returns any error from releasing the range.
func (a *Arena) Close() error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closed = true
	return a.releaseRangeLocked()
}

func (a *Arena) releaseRangeLocked() error {
	if !a.closed || a.used != 0 || a.mem == nil {
		return nil
	}
	err := unreserveArena(a.mem)
	a.mem, a.free = nil, nil
	return err
}

// place copies code into enough free pages, relocates it and makes the pages
// executable. The result spans whole pages and is owned until release.
func (a *Arena) place(code []byte, relocate func([]byte) error) ([]byte, error) {
	pages, err := a.take(len(code), 1)
	if err != nil {
		return nil, err
	}
	if err = commitPages(pages); err == nil {
		err = fillCode(pages, code, relocate)
	}
	if err != nil {
		_ = a.release(pages)
		return nil, err
	}
	return pages, nil
}

// take removes the first free run that fits size bytes and counts codes
// Codes as holding pages.
func (a *Arena) take(size, codes int) ([]byte, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || a.mem == nil {
		return nil, ErrClosed
	}
	want := (size + a.page - 1) / a.page
	for i, run := range a.free {
		if run.pages < want {
			continue
		}
		if run.pages == want {
			a.free = slices.Delete(a.free, i, i+1)
		} else {
			a.free[i] = pageRun{run.start + want, run.pages - want}
		}
		a.used += codes
		start, end := run.start*a.page, (run.start+want)*a.page
		return a.mem[start:end:end], nil
	}
	return nil, fmt.Errorf("%d bytes: %w", size, ErrArenaFull)
}

// release returns pages from take to the free runs. Pages that cannot be
// made inaccessible again are not reused.
func (a *Arena) release(pages []byte) error { return a.releasePages(pages, 1) }

// releasePages is release for pages that codes Codes held.
func (a *Arena) releasePages(pages []byte, codes int) error {
	err := decommitPages(pages)
	a.mu.Lock()
	defer a.mu.Unlock()
	if err == nil {
		start := int(uintptr(unsafe.Pointer(&pages[0]))-uintptr(unsafe.Pointer(&a.mem[0]))) / a.page
		a.insertLocked(pageRun{start, len(pages) / a.page})
	}
	a.used -= codes
	if rerr := a.releaseRangeLocked(); err == nil {
		err = rerr
	}
	return err
}

func (a *Arena) insertLocked(run pageRun) {
	i, _ := slices.BinarySearchFunc(a.free, run.start, func(r pageRun, start int) int { return r.start - start })
	if i > 0 && a.free[i-1].start+a.free[i-1].pages == run.start {
		i--
		a.free[i].pages += run.pages
	} else {
		a.free = slices.Insert(a.free, i, run)
	}
	if i+1 < len(a.free) && a.free[i].start+a.free[i].pages == a.free[i+1].start {
		a.free[i].pages += a.free[i+1].pages
		a.free = slices.Delete(a.free, i+1, i+2)
	}
}

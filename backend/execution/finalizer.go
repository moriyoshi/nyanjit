package execution

import (
	"runtime"
	"sync"
	"weak"

	"github.com/moriyoshi/nyanjit/backend"
)

// finalizerOwner counts the open Codes that share one Finalizers list. The
// list runs when the last of them is released, and never again.
type finalizerOwner struct {
	mu        sync.Mutex
	codes     int
	finalized bool
}

// finalizerOwners maps a Finalizers list, identified by its first element,
// to its owner. Every lowering of one program passes the same list, so
// Templates and Codes from one program find one owner. Keys are weak, and a
// cleanup drops an entry with its list: the registry neither keeps a list
// alive nor forgets that it has run while anything can still present it.
var finalizerOwners struct {
	sync.Mutex
	m map[weak.Pointer[backend.Finalizer]]*finalizerOwner
}

// ownerFor returns the owner of fs, or nil for an empty list.
func ownerFor(fs backend.Finalizers) *finalizerOwner {
	if len(fs) == 0 {
		return nil
	}
	key := weak.Make(&fs[0])
	finalizerOwners.Lock()
	defer finalizerOwners.Unlock()
	o := finalizerOwners.m[key]
	if o == nil {
		if finalizerOwners.m == nil {
			finalizerOwners.m = make(map[weak.Pointer[backend.Finalizer]]*finalizerOwner)
		}
		o = new(finalizerOwner)
		finalizerOwners.m[key] = o
		runtime.AddCleanup(&fs[0], dropFinalizerOwner, key)
	}
	return o
}

func dropFinalizerOwner(key weak.Pointer[backend.Finalizer]) {
	finalizerOwners.Lock()
	delete(finalizerOwners.m, key)
	finalizerOwners.Unlock()
}

// acquire counts a Code about to be created. It fails once the list has run.
func (o *finalizerOwner) acquire() bool {
	if o == nil {
		return true
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.finalized {
		return false
	}
	o.codes++
	return true
}

// cancel undoes acquire for a Code that was not created. It never runs the
// list: only closing a Code does.
func (o *finalizerOwner) cancel() {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.codes--
	o.mu.Unlock()
}

// release ends one Code's share, and the last share runs fs.
func (o *finalizerOwner) release(fs backend.Finalizers) {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.codes--
	last := o.codes == 0
	o.finalized = o.finalized || last
	o.mu.Unlock()
	if last {
		fs.Finalize()
	}
}

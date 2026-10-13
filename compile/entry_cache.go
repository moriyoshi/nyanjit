package compile

import (
	"sync"

	"github.com/moriyoshi/nyanjit/backend/abi"
)

// Allocation reads entry geometry; public reports get separately owned data.
type entryPlanKey struct {
	architecture string
	flavor       abi.Flavor
	names        [16]string
	result       string
	words        [16]int
	count        int
}

var entryPlans = struct {
	sync.RWMutex
	values map[entryPlanKey]abiEntryLayout
}{values: make(map[entryPlanKey]abiEntryLayout)}

func cachedEntryLayout(t machineTarget, words []int) (abiEntryLayout, error) {
	return memoEntryLayout(nil, t, words)
}

// memoEntryLayout is cachedEntryLayout, first asking memo when it is not
// nil.
func memoEntryLayout(memo *tableMemo, t machineTarget, words []int) (abiEntryLayout, error) {
	if len(words) > 16 {
		return entryLayout(t, words)
	}
	key := entryPlanKey{architecture: t.Architecture(), flavor: t.ABIFlavor(), result: t.RegisterName(t.ResultRegister()), count: len(words)}
	copy(key.words[:], words)
	if t.Architecture() != "386" && len(words) <= abi.MaxArgumentsFor(t.Architecture()) {
		for i := range words {
			if r, ok := t.ParamRegister(i); ok {
				key.names[i] = t.RegisterName(r)
			}
		}
	}
	if memo != nil {
		for i := range memo.entries {
			if m := &memo.entries[i]; m.ok && m.key == key {
				return m.value, nil
			}
		}
	}
	entryPlans.RLock()
	v, ok := entryPlans.values[key]
	entryPlans.RUnlock()
	if ok {
		if memo != nil {
			m := &memo.entries[memo.nextEntry]
			m.value, m.key, m.ok = v, key, true
			memo.nextEntry = (memo.nextEntry + 1) % len(memo.entries)
		}
		return v, nil
	}
	v, err := entryLayout(t, words)
	if err == nil {
		v.arguments = v.arguments[:len(v.arguments):len(v.arguments)]
		for i := range v.arguments {
			w := v.arguments[i].Words
			v.arguments[i].Words = w[:len(w):len(w)]
		}
		w := v.result.Words
		v.result.Words = w[:len(w):len(w)]
		entryPlans.Lock()
		if len(entryPlans.values) < 512 {
			entryPlans.values[key] = v
		}
		entryPlans.Unlock()
	}
	return v, err
}

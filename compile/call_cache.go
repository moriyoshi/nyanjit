package compile

import (
	"sync"

	"github.com/moriyoshi/nyanjit/backend/abi"
)

// Call effects used by lowering are immutable. Public PreflightCall continues
// to return independent slices. Keep this cache bounded, without eviction.
type callPlanKey struct {
	architecture string
	kind         abi.CallKind
	abi          abi.Flavor
	count        int
	wide         uint16
}

var callPlans = struct {
	sync.RWMutex
	values map[callPlanKey]abi.CallEffect
}{values: make(map[callPlanKey]abi.CallEffect)}

func cachedCallEffect(spec abi.CallSpec) (abi.CallEffect, error) {
	key := callPlanKey{architecture: spec.Architecture, kind: spec.Kind, abi: spec.ABI, count: spec.ArgumentCount}
	cacheable := key.count >= 0 && key.count <= 16 && (spec.ArgumentWords == nil || len(spec.ArgumentWords) == key.count)
	for i, words := range spec.ArgumentWords {
		if words == 2 && i < 16 {
			key.wide |= 1 << i
		} else if words != 1 {
			cacheable = false
		}
	}
	if cacheable {
		callPlans.RLock()
		effect, ok := callPlans.values[key]
		callPlans.RUnlock()
		if ok {
			return effect, nil
		}
	}
	effect, err := PreflightCall(spec)
	if err == nil && cacheable {
		clip := func(s []string) []string { return s[:len(s):len(s)] }
		effect.ArgumentRegisters = clip(effect.ArgumentRegisters)
		effect.ClobberedRegisters = clip(effect.ClobberedRegisters)
		effect.ClobberedFPRegisters = clip(effect.ClobberedFPRegisters)
		effect.ClobberedSpecialRegisters = clip(effect.ClobberedSpecialRegisters)
		effect.ArgumentLocations = effect.ArgumentLocations[:len(effect.ArgumentLocations):len(effect.ArgumentLocations)]
		for i := range effect.ArgumentLocations {
			w := effect.ArgumentLocations[i].Words
			effect.ArgumentLocations[i].Words = w[:len(w):len(w)]
		}
		w := effect.ResultLocation.Words
		effect.ResultLocation.Words = w[:len(w):len(w)]
		callPlans.Lock()
		if len(callPlans.values) < 512 {
			callPlans.values[key] = effect
		}
		callPlans.Unlock()
	}
	return effect, err
}

package machine

import "github.com/moriyoshi/nyanjit/backend"

// Lookup returns a variable's home in a region. Register overrides are sparse;
// other visible variables retain their globally assigned spill slot.
func (p *HomePlan) Lookup(region, variable int) (Home, bool) {
	if p == nil {
		return Home{}, false
	}
	if p.Regions != nil || p.RegisterOverrides == nil {
		h, ok := p.Regions[region][variable]
		return h, ok
	}
	if h, ok := p.RegisterOverrides[region][variable]; ok {
		return h, true
	}
	if variable < 0 || variable >= len(p.Slots) || variable >= len(p.VariableRegions) {
		return Home{}, false
	}
	here, ok := p.RegionBounds[region]
	owner, owned := p.RegionBounds[p.VariableRegions[variable]]
	if !ok || !owned || here[0] < owner[0] || here[1] > owner[1] {
		return Home{}, false
	}
	bits := uint8(0)
	if variable < len(p.VariableBits) {
		bits = p.VariableBits[variable]
	}
	return Home{Slot: p.Slots[variable], Register: -1, Bits: bits}, true
}

// RegisterReserved tests the precomputed per-region reservation mask. Legacy
// hand-built plans without masks remain supported.
func (p *HomePlan) RegisterReserved(region int, r backend.Register) bool {
	if p == nil {
		return false
	}
	if p.ReservedRegions != nil {
		mask := p.ReservedRegions[region]
		return mask[int(r)/64]&(uint64(1)<<(uint(r)%64)) != 0
	}
	homes := p.Regions
	if homes == nil {
		homes = p.RegisterOverrides
	}
	for _, h := range homes[region] {
		if h.Register == int(r) {
			return true
		}
	}
	return false
}

// NeedsFloating reports whether any scope has an FP home. Plans produced by
// the compiler cache this once; legacy manually constructed plans are scanned.
func (p *HomePlan) NeedsFloating() bool {
	if p == nil {
		return false
	}
	if p.ReservedRegions != nil {
		return p.HasFloatingHomes
	}
	for _, floating := range p.FloatHomes {
		if floating {
			return true
		}
	}
	return false
}

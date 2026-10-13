package compile

import "github.com/moriyoshi/nyanjit/backend/machine"

// No control buffer escapes into the final physical program.
type controlBuffers struct {
	history                      struct{ fragments, exportSlots, exportHomes, results, positions, incoming, resultRegisters, resultWords, resultWordCounts, flagReports, bound, labels, steps, labelSlots, labelRegisters, labelWords machine.BufferHistory }
	fragments                    [][]physicalInsn
	exportSlots                  []map[int]int
	exportHomes                  []map[int]exportLocation
	results, positions, incoming []int
	resultRegisters              []physReg
	resultWords                  [][2]physReg
	resultWordCounts             []uint8
	flagReports                  []flagState
	bound, labels                []bool
	steps                        []emissionStep
	labelSlots, labelRegisters   [][]int
	labelWords                   [][]uint8
	// A pass's segment ops and segments, its fragment copies, the fragment
	// bodies they are copied from and its fragment emitters. They live
	// until the pass ends; the arenas keep their last chunk.
	arenas struct {
		history   struct{ ops, segments, fragments, bodies, emitters machine.BufferHistory }
		ops       []instruction
		segments  []builderSegment
		fragments []physicalInsn
		bodies    []locatedInsn
		emitters  []physicalEmitter
	}
	// The export maps a pass takes, kept for the next pass, the number it
	// took and the most entries one held.
	maps struct {
		homes                []map[int]exportLocation
		slots                []map[int]int
		homesUsed, slotsUsed int
		peak                 int
	}
}

// takeMap returns an empty map from pool, which keeps the maps a pass takes
// for the next pass; used counts the pass's maps.
func takeMap[K comparable, V any](pool *[]map[K]V, used *int, hint int) map[K]V {
	if *used < len(*pool) {
		m := (*pool)[*used]
		*used++
		clear(m)
		return m
	}
	m := make(map[K]V, hint)
	*pool = append(*pool, m)
	*used++
	return m
}

func (b *controlBuffers) mapBytes() int {
	return len(b.maps.homes)*retainedMapBytes[int, exportLocation](b.maps.peak) + len(b.maps.slots)*retainedMapBytes[int, int](b.maps.peak)
}

// retained reports the retained bytes and whether a buffer exceeds its
// limit: arenaLimit for the fragment and body arenas, scratchBufferLimit
// for the others.
func (b *controlBuffers) retained(arenaLimit int) (int, bool) {
	total := b.mapBytes()
	big := false
	for _, n := range []int{bufferBytes(b.fragments), bufferBytes(b.exportSlots), bufferBytes(b.exportHomes), bufferBytes(b.results), bufferBytes(b.positions), bufferBytes(b.incoming), bufferBytes(b.resultRegisters), bufferBytes(b.resultWords), bufferBytes(b.resultWordCounts), bufferBytes(b.flagReports), bufferBytes(b.bound), bufferBytes(b.labels), bufferBytes(b.steps), bufferBytes(b.labelSlots), bufferBytes(b.labelRegisters), bufferBytes(b.labelWords), bufferBytes(b.arenas.ops), bufferBytes(b.arenas.segments), bufferBytes(b.arenas.emitters)} {
		total += n
		big = big || n > scratchBufferLimit
	}
	for _, n := range []int{bufferBytes(b.arenas.fragments), bufferBytes(b.arenas.bodies)} {
		total += n
		big = big || n > arenaLimit
	}
	return total, big
}

// arenasLarge reports whether the fragment or body arena exceeds
// scratchBufferLimit, the demand recurring large lowerings keep.
func (b *controlBuffers) arenasLarge() bool {
	return bufferBytes(b.arenas.fragments) > scratchBufferLimit || bufferBytes(b.arenas.bodies) > scratchBufferLimit
}
func (b *controlBuffers) release(poison bool) {
	if poison {
		for _, buf := range b.fragments[:cap(b.fragments)] {
			for i := range buf[:cap(buf)] {
				buf[:cap(buf)][i] = physicalInsn{Op: 255}
			}
		}
		for _, m := range b.exportSlots[:cap(b.exportSlots)] {
			clear(m)
		}
		for _, m := range b.exportHomes[:cap(b.exportHomes)] {
			clear(m)
		}
		for _, rows := range [][][]int{b.labelSlots, b.labelRegisters} {
			for _, row := range rows[:cap(rows)] {
				for i := range row[:cap(row)] {
					row[:cap(row)][i] = -0x5a5a
				}
			}
		}
		for _, row := range b.labelWords[:cap(b.labelWords)] {
			for i := range row[:cap(row)] {
				row[:cap(row)][i] = 255
			}
		}
		for i := range b.resultRegisters[:cap(b.resultRegisters)] {
			b.resultRegisters[:cap(b.resultRegisters)][i] = 255
		}
		for i := range b.resultWords[:cap(b.resultWords)] {
			b.resultWords[:cap(b.resultWords)][i] = [2]physReg{255, 255}
		}
		for _, buf := range [][]bool{b.bound, b.labels} {
			for i := range buf[:cap(buf)] {
				buf[:cap(buf)][i] = true
			}
		}
	}
	a := &b.arenas
	if poison {
		for i := range a.fragments[:cap(a.fragments)] {
			a.fragments[:cap(a.fragments)][i] = physicalInsn{Op: 255}
		}
		for i := range a.bodies[:cap(a.bodies)] {
			a.bodies[:cap(a.bodies)][i] = locatedInsn{ins: physicalInsn{Op: 255}, pc: -0x5a5a}
		}
		for i := range a.ops[:cap(a.ops)] {
			a.ops[:cap(a.ops)][i] = instruction{Op: 255, A: -0x5a5a, B: -0x5a5a, C: -0x5a5a}
		}
	} else {
		clear(a.fragments[:cap(a.fragments)])
		clear(a.bodies[:cap(a.bodies)])
		clear(a.ops[:cap(a.ops)])
	}
	clear(a.segments[:cap(a.segments)])
	clear(a.emitters[:cap(a.emitters)])
	a.ops, a.segments, a.fragments, a.bodies, a.emitters = a.ops[:0], a.segments[:0], a.fragments[:0], a.bodies[:0], a.emitters[:0]
	clear(b.fragments[:cap(b.fragments)])
	clear(b.exportSlots[:cap(b.exportSlots)])
	clear(b.exportHomes[:cap(b.exportHomes)])
	clear(b.steps[:cap(b.steps)])
	clear(b.labelSlots[:cap(b.labelSlots)])
	clear(b.labelRegisters[:cap(b.labelRegisters)])
	clear(b.labelWords[:cap(b.labelWords)])
	b.fragments = b.fragments[:0]
	b.exportSlots = b.exportSlots[:0]
	b.exportHomes = b.exportHomes[:0]
	b.steps = b.steps[:0]
	b.labelSlots = b.labelSlots[:0]
	b.labelRegisters = b.labelRegisters[:0]
	b.labelWords = b.labelWords[:0]
	if poison {
		for _, v := range [][]int{b.results, b.positions, b.incoming} {
			v = v[:cap(v)]
			for i := range v {
				v[i] = -0x5a5a
			}
		}
		for i := range b.flagReports[:cap(b.flagReports)] {
			b.flagReports[:cap(b.flagReports)][i] = flagState{Modified: 255}
		}
		for i := range b.resultWordCounts[:cap(b.resultWordCounts)] {
			b.resultWordCounts[:cap(b.resultWordCounts)][i] = 255
		}
	}
}

// decay drops buffers over their limit (arenaLimit for the fragment and body
// arenas, see retained) or with excess capacity for their recent demand.
func (b *controlBuffers) decay(arenaLimit int) {
	b.maps.homes, b.maps.slots, b.maps.peak = nil, nil, 0
	decayBuffer(&b.fragments, &b.history.fragments)
	decayBuffer(&b.exportSlots, &b.history.exportSlots)
	decayBuffer(&b.exportHomes, &b.history.exportHomes)
	decayBuffer(&b.results, &b.history.results)
	decayBuffer(&b.positions, &b.history.positions)
	decayBuffer(&b.incoming, &b.history.incoming)
	decayBuffer(&b.resultRegisters, &b.history.resultRegisters)
	decayBuffer(&b.resultWords, &b.history.resultWords)
	decayBuffer(&b.resultWordCounts, &b.history.resultWordCounts)
	decayBuffer(&b.flagReports, &b.history.flagReports)
	decayBuffer(&b.bound, &b.history.bound)
	decayBuffer(&b.labels, &b.history.labels)
	decayBuffer(&b.steps, &b.history.steps)
	decayBuffer(&b.labelSlots, &b.history.labelSlots)
	decayBuffer(&b.labelRegisters, &b.history.labelRegisters)
	decayBuffer(&b.labelWords, &b.history.labelWords)
	decayBuffer(&b.arenas.ops, &b.arenas.history.ops)
	decayBuffer(&b.arenas.segments, &b.arenas.history.segments)
	decayBufferWithin(&b.arenas.fragments, &b.arenas.history.fragments, arenaLimit)
	decayBufferWithin(&b.arenas.bodies, &b.arenas.history.bodies, arenaLimit)
	decayBuffer(&b.arenas.emitters, &b.arenas.history.emitters)
}

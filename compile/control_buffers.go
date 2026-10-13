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
}

func (b *controlBuffers) retained() (int, bool) {
	total := 0
	big := false
	for _, n := range []int{bufferBytes(b.fragments), bufferBytes(b.exportSlots), bufferBytes(b.exportHomes), bufferBytes(b.results), bufferBytes(b.positions), bufferBytes(b.incoming), bufferBytes(b.resultRegisters), bufferBytes(b.resultWords), bufferBytes(b.resultWordCounts), bufferBytes(b.flagReports), bufferBytes(b.bound), bufferBytes(b.labels), bufferBytes(b.steps), bufferBytes(b.labelSlots), bufferBytes(b.labelRegisters), bufferBytes(b.labelWords)} {
		total += n
		big = big || n > scratchBufferLimit
	}
	return total, big
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

func (b *controlBuffers) decay() {
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
}

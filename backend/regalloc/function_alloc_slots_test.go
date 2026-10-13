package regalloc

import (
	"errors"
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"testing"
)

// slotRecorder tracks the highest slot word a function allocation touches.
type slotRecorder struct {
	values []Value
	end    int
}

func (r *slotRecorder) storage(v int) int {
	x := r.values[v]
	if x.StorageWords != 0 {
		return int(x.StorageWords)
	}
	if x.Words == 0 || x.Class == backend.Float {
		return 1
	}
	return int(x.Words)
}
func (r *slotRecorder) Spill(v int, slot int, _ Group)  { r.end = max(r.end, slot+r.storage(v)) }
func (r *slotRecorder) Reload(v int, _ Group, slot int) { r.end = max(r.end, slot+r.storage(v)) }
func (r *slotRecorder) LoadIncoming(int, Group, [2]int) {}
func (r *slotRecorder) Rematerialize(int, Group) error  { return nil }
func (r *slotRecorder) Move(int, Group, Group)          {}

// Values wider than two words must stay inside the frame: only vectors may
// need more storage words, and edge temporaries hold the widest value
// (security audit 2026-10-25, F21).
func TestBatchSlotsFitWideValues(t *testing.T) {
	use := func(v int) Operand { return Operand{Value: v} }
	def := func(v int) Operand { return Operand{Value: v, Def: true} }
	allocate := func(cfg Config, values []Value, blocks []Block, edges []Edge, ops [][]Operand) (*slotRecorder, int, error) {
		var fa Batch
		r := &slotRecorder{values: values}
		if err := fa.Allocate(cfg, buildFunction(values, blocks, edges, ops, nil, nil, abi.EntryLayout{}), r); err != nil {
			return nil, 0, err
		}
		defer fa.Finish()
		if err := fa.Entry(); err != nil {
			return nil, 0, err
		}
		next := 0
		for p := range ops {
			if err := fa.Before(p); err != nil {
				return nil, 0, err
			}
			if err := fa.After(p); err != nil {
				return nil, 0, err
			}
			for next < len(blocks) && blocks[next].End == p+1 {
				for e, edge := range edges {
					if edge.From == next {
						if err := fa.Edge(e); err != nil {
							return nil, 0, err
						}
					}
				}
				next++
			}
		}
		return r, fa.Slots(), nil
	}
	for name, values := range map[string][]Value{
		"general storage 4": {{}, {}, {StorageWords: 4}, {}},
		"general words 3":   {{}, {}, {Words: 3}, {}},
	} {
		ops := [][]Operand{{def(0)}, {def(1)}, {use(0)}, {use(1)}, {def(2)}, {def(3)}, {use(2)}, {use(3)}}
		_, _, err := allocate(Config{Registers: gprs(4), SlotLimit: 1 << 10}, values, []Block{{Start: 0, End: len(ops)}}, nil, ops)
		if !errors.Is(err, backend.ErrInvalidFunction) {
			t.Errorf("%s: %v, want ErrInvalidFunction", name, err)
		}
	}
	// Four-word vectors (128 bits in 32-bit slots) swap through memory on a
	// back edge, so edge temporaries carry whole vectors.
	vector := Value{Class: backend.Vector, StorageWords: 4}
	r, slots, err := allocate(Config{Registers: testRegisters(2, 1<<1), SlotLimit: 1 << 10},
		[]Value{vector, vector, vector, vector},
		[]Block{{Start: 0, End: 2}, {Start: 2, End: 4, Params: []int{2, 3}}, {Start: 4, End: 5}},
		[]Edge{{From: 0, To: 1, Args: []int{0, 1}}, {From: 1, To: 1, Args: []int{3, 2}}, {From: 1, To: 2}},
		[][]Operand{{def(0)}, {def(1)}, {use(2)}, {use(3)}, {use(2)}})
	if err != nil {
		t.Fatal(err)
	}
	if r.end > slots {
		t.Fatalf("vector edge swap touches slot word %d of a %d-slot frame", r.end, slots)
	}
}

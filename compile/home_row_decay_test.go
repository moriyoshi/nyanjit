package compile

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend/machine"
)

func TestHomeRowsUseSteadyDemandBand(t *testing.T) {
	b := homeBuffers{rows: []homeRow{{homes: make(map[int]machine.Home), modified: make(map[int]bool), ids: make([]int, 10, 30)}}}
	for i := 0; i < 10; i++ {
		b.rows[0].homes[i] = machine.Home{}
	}
	for i := 0; i < 100; i++ {
		b.used = true
		b.decay()
	}
	if b.rows[0].homes != nil || b.rows[0].ids != nil {
		t.Fatal("home row retained triple steady demand indefinitely")
	}
}

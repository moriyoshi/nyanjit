package execution

import (
	"errors"
	"testing"
)

func TestWordsRejectsNilAggregatePointers(t *testing.T) {
	for _, v := range []any{(*struct{ A uint64 })(nil), (*[8]uint32)(nil), (*[]uint64)(nil)} {
		t.Run("nil", func(t *testing.T) {
			defer func() {
				err, ok := recover().(error)
				if !ok || !errors.Is(err, ErrArgumentType) {
					t.Fatalf("nil aggregate pointer accepted: %v", err)
				}
			}()
			Words(v)
		})
	}
	// A nil slice value still has a live boxed header and is valid input.
	if words := Words([]uint64(nil)); len(words) != 1 || words[0] == 0 {
		t.Fatal("nil slice header rejected")
	}
}

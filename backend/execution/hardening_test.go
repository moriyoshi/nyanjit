package execution

import (
	"bytes"
	"errors"
	"testing"
)

func TestHardenMappingPlacement(t *testing.T) {
	body := []byte{1, 2, 3, 4}
	for n := 0; n < 256; n++ {
		image, offset, err := mappingBody(body, true, true, bytes.NewReader([]byte{byte(n)}))
		if err != nil || offset != (n+1)*16 || len(image) != offset+len(body)+1 || !bytes.Equal(image[offset:offset+len(body)], body) {
			t.Fatalf("slot%d offset%d image%d err%v", n, offset, len(image), err)
		}
		if bytes.Equal(image[:offset], make([]byte, offset)) {
			t.Fatal("prefix contains no traps")
		}
	}
	sentinel := errors.New("random unavailable")
	if _, _, err := mappingBody(body, true, false, errorReader{sentinel}); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	image, offset, err := mappingBody(body, false, false, errorReader{sentinel})
	if err != nil || offset != 0 || &image[0] != &body[0] {
		t.Fatal("default placement changed")
	}
}

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }

package arm64

import (
	"strconv"

	"github.com/moriyoshi/nyanjit/backend"
)

// registerNames names registers by ordinal for RegisterName and the
// policy's register sets; "" marks an ordinal that is no arm64 register.
var registerNames = func() (names [256]string) {
	for n := range uint8(32) {
		names[n] = "X" + strconv.Itoa(int(n))
		names[backend.VectorRegister(n)] = "V" + strconv.Itoa(int(n))
	}
	return
}()

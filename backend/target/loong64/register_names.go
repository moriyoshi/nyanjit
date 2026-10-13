package loong64

import (
	"strconv"

	"github.com/moriyoshi/nyanjit/backend"
)

// registerNames names registers by ordinal for RegisterName and the
// policy's register sets; "" marks an ordinal that is no loong64 register.
var registerNames = func() (names [256]string) {
	for n := range uint8(32) {
		names[n] = "R" + strconv.Itoa(int(n))
		names[backend.VectorRegister(n)] = "F" + strconv.Itoa(int(n))
	}
	return
}()

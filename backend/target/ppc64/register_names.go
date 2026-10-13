package ppc64

import (
	"strconv"

	"github.com/moriyoshi/nyanjit/backend"
)

// registerNamesCR names condition-register fields.
var registerNamesCR = func() (names [256]string) {
	for n := range names {
		names[n] = "CR" + strconv.Itoa(n)
	}
	return
}()

// registerNames names registers by ordinal for RegisterName and the
// policy's register sets; "" marks an ordinal that is no ppc64 register.
var registerNames = func() (names [256]string) {
	for n := range uint8(32) {
		names[n] = "R" + strconv.Itoa(int(n))
		names[backend.VectorRegister(n)] = "F" + strconv.Itoa(int(n))
	}
	return
}()

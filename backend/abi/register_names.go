package abi

import "strconv"

func makeRegisterNames(prefix string) (out [256]string) {
	for i := range out {
		out[i] = prefix + strconv.Itoa(i)
	}
	return
}

var registerNamesV = makeRegisterNames("V")
var registerNamesX = makeRegisterNames("X")

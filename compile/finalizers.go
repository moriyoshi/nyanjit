package compile

import "github.com/moriyoshi/nyanjit/backend"

// appendFinalizers appends a segment's Finalizers to the function's list. A
// list that comes whole from one segment is kept as it is, without a copy:
// execution counts the Codes of one program by its list, so every lowering of
// a sealed program must hand execution the program's own list.
func appendFinalizers(list, more backend.Finalizers) backend.Finalizers {
	switch {
	case len(more) == 0:
		return list
	case len(list) == 0:
		return more
	}
	return append(list[:len(list):len(list)], more...)
}

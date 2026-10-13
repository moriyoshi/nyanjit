package regalloc

import "errors"

var ErrInvalidFunction = errors.New("nyanjit: invalid function")
var ErrBackendLimit = errors.New("nyanjit: function exceeds backend limits")

package backend

import "errors"

// ErrInvalidFunction reports a malformed function or instruction sequence.
var ErrInvalidFunction = errors.New("nyanjit: invalid function")

// ErrBackendLimit reports a function that exceeds what the backend supports.
var ErrBackendLimit = errors.New("nyanjit: function exceeds backend limits")

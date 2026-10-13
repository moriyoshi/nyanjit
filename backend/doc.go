// Package backend holds the vocabulary that NyanJIT's lower layers share:
// physical register identities and sets, value register classes, and the
// errors lowering reports for invalid input and exceeded backend limits, and
// the Finalizers that release host resources generated code depends on.
// Packages such as machine and regalloc build on it without depending on each
// other.
package backend

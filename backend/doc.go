// Package backend holds the vocabulary that NyanJIT's lower layers share:
// physical register identities and sets, value register classes, and the
// errors lowering reports for invalid input and exceeded backend limits.
// Packages such as machine and regalloc build on it without depending on each
// other.
package backend

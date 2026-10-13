// Package instrument is the home of nyanjit's instrumentation: packages that
// measure, check or test generated code rather than produce it.
//
//   - workload builds deterministic programs for compile-time benchmarks and
//     the linear-scaling gate.
//   - abicheck calls generated code and reports registers its ABI requires it
//     to preserve but that it changed.
//   - irinterp is a reference interpreter for nyanjit IR.
//   - irconform is a conformance suite: it runs IR programs natively and in
//     irinterp and compares the results.
//   - jitbench holds the harnesses that compare NyanJIT's compile and run
//     times with sljit, TPDE and Cranelift, and a driver for them.
//
// Only tests, examples and other instrument packages may import these
// packages; production code must not. They depend on the whole compiler, and
// their APIs serve measurement first.
package instrument

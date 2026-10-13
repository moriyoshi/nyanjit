load("backend_parity.star", "run_arch")
load("targets.star", "targets")

def check_no_sse2(t):
    suite = "386-no-sse2"
    if not selected(t, suite):
        return
    # Build a softfloat Go test driver so Go itself can start without SSE2.
    # NyanJIT still requires SSE2. QEMU has no pentium4 model; qemu32
    # includes SSE2, and disabling SSE3 gives the required SSE2-only baseline.
    binary = "/cache/nyanjit-no-sse2-" + t.go_version
    built = sh(["env", "GOOS=linux", "GOARCH=386", "GO386=softfloat", "CGO_ENABLED=0",
                "/opt/go" + t.go_version + "/bin/go", "test", "-c", "-o", binary, "./compile"], timeout = 1200)
    check(t, suite, "build-probe", built)
    result = run(t, binary, args = ["-test.run=^Test386MissingSSE2Gate$", "-test.v"],
                 env = ["QEMU_CPU=qemu32,-sse2", "NYANJIT_TEST_NO_SSE2=1"], timeout = 120)
    check(t, suite, "reject-before-emission", result, contains = "ErrCPUUnsupported")

def main():
    for t in targets():
        if t.goos == "linux" and t.goarch == "386":
            check_no_sse2(t)
    run_arch("386", [("baseline", "qemu32,-sse3"), ("max", "max")])

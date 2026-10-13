# Flip capability expectations here as ports become executable.
GO_VERSIONS = ["1.26.9", "1.27.2"]
SUPPORTED = [
    ("linux", "amd64"), ("linux", "386"),
    ("linux", "arm64"), ("linux", "riscv64"),
    # PPC64 covers integer/FP execution, hardware FMA, syscalls, C calls,
    # and managed Go frames.
    ("linux", "ppc64"), ("linux", "ppc64le"),
]
PAIRS = [
    ("linux", "amd64"), ("linux", "386"), ("linux", "arm64"),
    ("linux", "riscv64"), ("linux", "arm"), ("linux", "ppc64"),
    ("linux", "ppc64le"), ("linux", "mips64"), ("linux", "mips64le"),
    ("linux", "loong64"),
    ("windows", "386"), ("windows", "amd64"), ("windows", "arm64"),
    ("freebsd", "386"), ("freebsd", "amd64"), ("freebsd", "arm"), ("freebsd", "arm64"),
    ("netbsd", "386"), ("netbsd", "amd64"), ("netbsd", "arm"), ("netbsd", "arm64"),
    ("openbsd", "386"), ("openbsd", "amd64"), ("openbsd", "arm"),
    ("openbsd", "arm64"), ("openbsd", "ppc64"), ("openbsd", "riscv64"),
    ("dragonfly", "amd64"), ("solaris", "amd64"), ("illumos", "amd64"),
]

def targets():
    result = []
    for goos, goarch in PAIRS:
        variants = [{}]
        if goos == "linux" and goarch == "arm":
            variants = [{"GOARM": x} for x in ["5", "6", "7"]]
        elif goos == "linux" and goarch in ["ppc64", "ppc64le"]:
            variants = [{"GOPPC64": x} for x in ["power8", "power9", "power10"]]
        elif goos == "linux" and goarch in ["mips64", "mips64le"]:
            variants = [{"GOMIPS64": x} for x in ["hardfloat", "softfloat"]]
        for go_version in GO_VERSIONS:
            for variant in variants:
                result.append(target(goos, goarch, go_version = go_version, **variant))
    return result

def capability(t):
    if (t.goos, t.goarch) in SUPPORTED:
        return "supported"
    if runner(t) == "no runner":
        return "no runner"
    return "unsupported"

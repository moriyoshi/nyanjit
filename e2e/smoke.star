load("targets.star", "targets", "capability")

def run_smoke(t):
    if not selected(t, "smoke"):
        return
    binary = build_example(t, "./examples/add")
    if capability(t) == "no runner":
        skip(t, "smoke", "add", "no runner: " + runner_reason(t))
        return
    result = run(t, binary, timeout = 2400)
    if capability(t) == "supported":
        check(t, "smoke", "add", result, contains = "42")
    else:
        check(t, "smoke", "add", result, expect = "unsupported")

def main():
    jobs = []
    for t in targets():
        if selected(t, "smoke"):
            jobs.append(go(run_smoke, t))
    for job in jobs:
        join(job, timeout = 3600)

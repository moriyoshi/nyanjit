package harness

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

type promise struct {
	id     uint64
	mu     sync.Mutex
	done   chan struct{}
	val    starlark.Value
	err    error
	cancel context.CancelFunc
}

var nextPromise atomic.Uint64

func newPromise(cancel context.CancelFunc) *promise {
	return &promise{id: nextPromise.Add(1), done: make(chan struct{}), cancel: cancel}
}
func (p *promise) settle(v starlark.Value, e error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	select {
	case <-p.done:
		return
	default:
	}
	if v == nil {
		v = starlark.None
	}
	p.val, p.err = v, e
	close(p.done)
}
func (p *promise) await(ctx context.Context) (starlark.Value, error) {
	select {
	case <-p.done:
		return p.val, p.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (p *promise) String() string        { return fmt.Sprintf("<promise %d>", p.id) }
func (p *promise) Type() string          { return "promise" }
func (p *promise) Freeze()               {}
func (p *promise) Truth() starlark.Bool  { return starlark.True }
func (p *promise) Hash() (uint32, error) { return uint32(p.id), nil }
func (p *promise) AttrNames() []string   { return []string{"done", "signal", "reject", "cancel"} }
func (p *promise) Attr(name string) (starlark.Value, error) {
	switch name {
	case "done":
		return starlark.NewBuiltin("done", func(_ *starlark.Thread, _ *starlark.Builtin, _ starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
			select {
			case <-p.done:
				return starlark.True, nil
			default:
				return starlark.False, nil
			}
		}), nil
	case "signal":
		return starlark.NewBuiltin("signal", func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
			if len(args) != 1 {
				return nil, fmt.Errorf("signal: want value")
			}
			p.settle(args[0], nil)
			return starlark.None, nil
		}), nil
	case "reject":
		return starlark.NewBuiltin("reject", func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
			if len(args) != 1 {
				return nil, fmt.Errorf("reject: want reason")
			}
			p.settle(nil, fmt.Errorf("%s", args[0]))
			return starlark.None, nil
		}), nil
	case "cancel":
		return starlark.NewBuiltin("cancel", func(_ *starlark.Thread, _ *starlark.Builtin, _ starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
			if p.cancel != nil {
				p.cancel()
			}
			p.settle(starlark.None, nil)
			return starlark.None, nil
		}), nil
	}
	return nil, nil
}
func seconds(v int) time.Duration {
	if v <= 0 {
		return 0
	}
	return time.Duration(v) * time.Second
}
func waitContext(t *starlark.Thread, sec int) (context.Context, context.CancelFunc, error) {
	if sec <= 0 {
		return nil, nil, fmt.Errorf("wait requires positive timeout")
	}
	c, cancel := context.WithTimeout(contextOf(t), seconds(sec))
	return c, cancel, nil
}
func parseTarget(v starlark.Value) (Target, error) {
	a, ok := v.(starlark.HasAttrs)
	if !ok {
		return Target{}, fmt.Errorf("expected target")
	}
	get := func(n string) string { x, _ := a.Attr(n); s, _ := starlark.AsString(x); return s }
	return Target{OS: get("goos"), Arch: get("goarch"), GoVersion: get("go_version"), GOARM: get("GOARM"), GOPPC64: get("GOPPC64"), GOMIPS64: get("GOMIPS64")}, nil
}
func targetValue(t Target) starlark.Value {
	return starlarkstruct.FromStringDict(starlarkstruct.Default, starlark.StringDict{"goos": starlark.String(t.OS), "goarch": starlark.String(t.Arch), "go_version": starlark.String(t.GoVersion), "GOARM": starlark.String(t.GOARM), "GOPPC64": starlark.String(t.GOPPC64), "GOMIPS64": starlark.String(t.GOMIPS64)})
}
func execValue(r ExecResult) starlark.Value {
	return starlarkstruct.FromStringDict(starlarkstruct.Default, starlark.StringDict{"output": starlark.String(r.Output), "exit_code": starlark.MakeInt(r.ExitCode), "status": starlark.String(r.Status), "reason": starlark.String(r.Reason), "seconds": starlark.Float(r.Duration.Seconds())})
}
func field(v starlark.Value, n string) starlark.Value {
	a, ok := v.(starlark.HasAttrs)
	if !ok {
		return starlark.None
	}
	x, _ := a.Attr(n)
	return x
}
func fieldString(v starlark.Value, n string) string { x, _ := starlark.AsString(field(v, n)); return x }
func stringsOf(v starlark.Value) ([]string, error) {
	var out []string
	it := starlark.Iterate(v)
	if it == nil {
		return nil, fmt.Errorf("expected list")
	}
	defer it.Done()
	var x starlark.Value
	for it.Next(&x) {
		s, ok := starlark.AsString(x)
		if !ok {
			return nil, fmt.Errorf("expected string, got %s", x.Type())
		}
		out = append(out, s)
	}
	return out, nil
}
func (h *Harness) async(ctx context.Context, fn func(context.Context) (starlark.Value, error)) *promise {
	c, cancel := context.WithCancel(ctx)
	p := newPromise(cancel)
	go func() { v, e := fn(c); p.settle(v, e); cancel() }()
	return p
}
func (h *Harness) predeclared() starlark.StringDict {
	b := func(n string, f func(*starlark.Thread, *starlark.Builtin, starlark.Tuple, []starlark.Tuple) (starlark.Value, error)) *starlark.Builtin {
		return starlark.NewBuiltin(n, f)
	}
	d := starlark.StringDict{}
	d["getenv"] = b("getenv", func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kw []starlark.Tuple) (starlark.Value, error) {
		var name, fallback string
		if err := starlark.UnpackArgs("getenv", args, kw, "name", &name, "default?", &fallback); err != nil {
			return nil, err
		}
		value, ok := os.LookupEnv(name)
		if !ok {
			value = fallback
		}
		return starlark.String(value), nil
	})
	d["target"] = b("target", func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kw []starlark.Tuple) (starlark.Value, error) {
		var osname, arch, ver, arm, ppc, mips string
		ver = "1.27.2"
		if e := starlark.UnpackArgs("target", args, kw, "goos", &osname, "goarch", &arch, "go_version?", &ver, "GOARM?", &arm, "GOPPC64?", &ppc, "GOMIPS64?", &mips); e != nil {
			return nil, e
		}
		return targetValue(Target{OS: osname, Arch: arch, GoVersion: ver, GOARM: arm, GOPPC64: ppc, GOMIPS64: mips}), nil
	})
	d["selected"] = b("selected", func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kw []starlark.Tuple) (starlark.Value, error) {
		var v starlark.Value
		var suite string
		if e := starlark.UnpackArgs("selected", args, kw, "target", &v, "suite", &suite); e != nil {
			return nil, e
		}
		t, e := parseTarget(v)
		if e != nil {
			return nil, e
		}
		return starlark.Bool(h.selected(t, suite)), nil
	})
	d["supported"] = b("supported", func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kw []starlark.Tuple) (starlark.Value, error) {
		var v starlark.Value
		if e := starlark.UnpackArgs("supported", args, kw, "target", &v); e != nil {
			return nil, e
		}
		t, e := parseTarget(v)
		if e != nil {
			return nil, e
		}
		return starlark.Bool(t.Supported()), nil
	})
	d["runner"] = b("runner", func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kw []starlark.Tuple) (starlark.Value, error) {
		var v starlark.Value
		if e := starlark.UnpackArgs("runner", args, kw, "target", &v); e != nil {
			return nil, e
		}
		t, e := parseTarget(v)
		if e != nil {
			return nil, e
		}
		return starlark.String(t.Runner()), nil
	})
	d["runner_reason"] = b("runner_reason", func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kw []starlark.Tuple) (starlark.Value, error) {
		var v starlark.Value
		if e := starlark.UnpackArgs("runner_reason", args, kw, "target", &v); e != nil {
			return nil, e
		}
		t, e := parseTarget(v)
		if e != nil {
			return nil, e
		}
		return starlark.String(vmRunnerReason(t)), nil
	})
	d["go"] = b("go", func(t *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kw []starlark.Tuple) (starlark.Value, error) {
		if len(args) == 0 || len(kw) > 0 {
			return nil, fmt.Errorf("go(fn, *args)")
		}
		fn, ok := args[0].(starlark.Callable)
		if !ok {
			return nil, fmt.Errorf("go: expected callable")
		}
		fn.Freeze()
		a := append(starlark.Tuple(nil), args[1:]...)
		a.Freeze()
		return h.async(contextOf(t), func(c context.Context) (starlark.Value, error) { return h.invoke(c, fn, a) }), nil
	})
	d["promise"] = b("promise", func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kw []starlark.Tuple) (starlark.Value, error) {
		if len(args) > 0 || len(kw) > 0 {
			return nil, fmt.Errorf("promise() takes no arguments")
		}
		return newPromise(nil), nil
	})
	d["join"] = b("join", func(t *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kw []starlark.Tuple) (starlark.Value, error) {
		var timeout int = 3600
		for _, pair := range kw {
			n, _ := starlark.AsString(pair[0])
			if n != "timeout" {
				return nil, fmt.Errorf("join: unexpected %s", n)
			}
			timeout, _ = starlark.AsInt32(pair[1])
		}
		c, cancel, e := waitContext(t, timeout)
		if e != nil {
			return nil, e
		}
		defer cancel()
		out := make([]starlark.Value, len(args))
		for i, v := range args {
			p, ok := v.(*promise)
			if !ok {
				return nil, fmt.Errorf("join: expected promise")
			}
			out[i], e = p.await(c)
			if e != nil {
				return nil, e
			}
		}
		if len(out) == 1 {
			return out[0], nil
		}
		return starlark.NewList(out), nil
	})
	d["select"] = b("select", func(t *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kw []starlark.Tuple) (starlark.Value, error) {
		var timeout int = 3600
		for _, pair := range kw {
			n, _ := starlark.AsString(pair[0])
			if n != "timeout" {
				return nil, fmt.Errorf("select: unexpected %s", n)
			}
			timeout, _ = starlark.AsInt32(pair[1])
		}
		if len(args) == 0 {
			return nil, fmt.Errorf("select: no promises")
		}
		c, cancel, e := waitContext(t, timeout)
		if e != nil {
			return nil, e
		}
		defer cancel()
		cases := make([]reflect.SelectCase, 0, len(args)+1)
		for _, v := range args {
			p, ok := v.(*promise)
			if !ok {
				return nil, fmt.Errorf("select: expected promise")
			}
			cases = append(cases, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(p.done)})
		}
		cases = append(cases, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(c.Done())})
		i, _, _ := reflect.Select(cases)
		if i == len(args) {
			return nil, c.Err()
		}
		return args[i], nil
	})
	d["poll"] = b("poll", func(t *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kw []starlark.Tuple) (starlark.Value, error) {
		var fn starlark.Value
		var timeout, interval int = 1, 1
		if e := starlark.UnpackArgs("poll", args, kw, "check", &fn, "timeout", &timeout, "interval?", &interval); e != nil {
			return nil, e
		}
		if timeout <= 0 || interval <= 0 {
			return nil, fmt.Errorf("poll: positive timeout and interval required")
		}
		fn.Freeze()
		return h.async(contextOf(t), func(parent context.Context) (starlark.Value, error) {
			c, cancel := context.WithTimeout(parent, seconds(timeout))
			defer cancel()
			ticker := time.NewTicker(seconds(interval))
			defer ticker.Stop()
			for {
				v, e := h.invoke(c, fn, nil)
				if e != nil {
					return nil, e
				}
				if v.Truth() {
					return starlark.True, nil
				}
				select {
				case <-ticker.C:
				case <-c.Done():
					if c.Err() == context.DeadlineExceeded {
						return starlark.False, nil
					}
					return nil, c.Err()
				}
			}
		}), nil
	})
	d["sh"] = b("sh", func(t *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kw []starlark.Tuple) (starlark.Value, error) {
		var argv starlark.Value
		var timeout int = 300
		if e := starlark.UnpackArgs("sh", args, kw, "argv", &argv, "timeout?", &timeout); e != nil {
			return nil, e
		}
		aa, e := stringsOf(argv)
		if e != nil || len(aa) == 0 {
			return nil, fmt.Errorf("sh: nonempty argv required")
		}
		c, cancel, e := waitContext(t, timeout)
		if e != nil {
			return nil, e
		}
		defer cancel()
		r, slotErr := h.withSlot(c, func() (ExecResult, error) {
			return runCommand(c, h.Root, nil, aa), nil
		})
		if slotErr != nil {
			return nil, slotErr
		}
		return execValue(r), nil
	})
	d["packages"] = b("packages", func(t *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kw []starlark.Tuple) (starlark.Value, error) {
		var tv starlark.Value
		if e := starlark.UnpackArgs("packages", args, kw, "target", &tv); e != nil {
			return nil, e
		}
		target, e := parseTarget(tv)
		if e != nil {
			return nil, e
		}
		c, cancel := context.WithTimeout(contextOf(t), 5*time.Minute)
		defer cancel()
		r, slotErr := h.withSlot(c, func() (ExecResult, error) {
			return h.goPackages(c, target), nil
		})
		if slotErr != nil {
			return nil, slotErr
		}
		if r.ExitCode != 0 {
			return nil, fmt.Errorf("go list: %s", r.Output)
		}
		p := strings.Fields(r.Output)
		out := make([]starlark.Value, len(p))
		for i, x := range p {
			out[i] = starlark.String(x)
		}
		return starlark.NewList(out), nil
	})
	d["build_tests"] = b("build_tests", func(t *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kw []starlark.Tuple) (starlark.Value, error) {
		var tv, pv starlark.Value
		var buildmode string
		var timeout int = 900
		if e := starlark.UnpackArgs("build_tests", args, kw, "target", &tv, "pkgs", &pv, "timeout?", &timeout, "buildmode?", &buildmode); e != nil {
			return nil, e
		}
		target, e := parseTarget(tv)
		if e != nil {
			return nil, e
		}
		pkgs, e := stringsOf(pv)
		if e != nil {
			return nil, e
		}
		if buildmode != "" && buildmode != "pie" {
			return nil, fmt.Errorf("build_tests: unsupported buildmode %q", buildmode)
		}
		out := make([]starlark.Value, 0, len(pkgs))
		for _, pkg := range pkgs {
			dir := filepath.Join(h.buildRoot(), strings.ReplaceAll(target.Key(), "/", "_"))
			_ = os.MkdirAll(dir, 0755)
			bin := filepath.Join(dir, strings.NewReplacer("/", "_", ".", "_").Replace(pkg))
			if buildmode != "" {
				bin += "-" + buildmode
			}
			if target.OS == "windows" {
				bin += ".exe"
			}
			c, cancel, er := waitContext(t, timeout)
			if er != nil {
				return nil, er
			}
			start := time.Now()
			r, slotErr := h.withSlot(c, func() (ExecResult, error) {
				argv := []string{"test", "-c", "-o", bin}
				if buildmode != "" {
					argv = append(argv, "-buildmode="+buildmode)
				}
				return h.goPackageCommand(c, target, pkg, argv...), nil
			})
			if slotErr != nil {
				r = ExecResult{ExitCode: -1, Status: "fail", Reason: slotErr.Error()}
			}
			cancel()
			status := "pass"
			reason := ""
			if r.ExitCode != 0 {
				status = "fail"
				reason = "cross-compile failed"
			}
			h.record(Result{Target: target, Suite: "build", Name: pkg + buildModeSuffix(buildmode), Status: status, Output: r.Output, Reason: reason, Seconds: time.Since(start).Seconds()})
			if r.ExitCode == 0 {
				_, er = os.Stat(bin)
			} else {
				er = os.ErrNotExist
			}
			if er == nil {
				out = append(out, starlark.String(bin))
			}
		}
		return starlark.NewList(out), nil
	})
	d["run"] = b("run", func(t *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kw []starlark.Tuple) (starlark.Value, error) {
		var tv, av, ev starlark.Value
		var path string
		var timeout int = 300
		av = starlark.NewList(nil)
		ev = starlark.NewList(nil)
		if e := starlark.UnpackArgs("run", args, kw, "target", &tv, "binary", &path, "args?", &av, "env?", &ev, "timeout?", &timeout); e != nil {
			return nil, e
		}
		target, e := parseTarget(tv)
		if e != nil {
			return nil, e
		}
		aa, e := stringsOf(av)
		if e != nil {
			return nil, e
		}
		env, e := stringsOf(ev)
		if e != nil {
			return nil, e
		}
		c, cancel, e := waitContext(t, timeout)
		if e != nil {
			return nil, e
		}
		defer cancel()
		r, err := h.withSlot(c, func() (ExecResult, error) { return h.Backend.Run(c, target, path, aa, env) })
		if err != nil {
			return nil, err
		}
		return execValue(r), nil
	})
	d["build_example"] = b("build_example", func(t *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kw []starlark.Tuple) (starlark.Value, error) {
		var tv starlark.Value
		var pkg string
		if e := starlark.UnpackArgs("build_example", args, kw, "target", &tv, "pkg", &pkg); e != nil {
			return nil, e
		}
		target, e := parseTarget(tv)
		if e != nil {
			return nil, e
		}
		dir := filepath.Join(h.buildRoot(), strings.ReplaceAll(target.Key(), "/", "_"))
		_ = os.MkdirAll(dir, 0755)
		path := filepath.Join(dir, "example")
		if target.OS == "windows" {
			path += ".exe"
		}
		c, cancel := context.WithTimeout(contextOf(t), 10*time.Minute)
		defer cancel()
		r, slotErr := h.withSlot(c, func() (ExecResult, error) {
			return h.goCommand(c, target, "build", "-o", path, pkg), nil
		})
		if slotErr != nil {
			r = ExecResult{ExitCode: -1, Status: "fail", Reason: slotErr.Error()}
		}
		status := "pass"
		if r.ExitCode != 0 {
			status = "fail"
		}
		h.record(Result{Target: target, Suite: "smoke", Name: "build", Status: status, Output: r.Output, Reason: status})
		if r.ExitCode != 0 {
			return nil, fmt.Errorf("build example: %s", r.Output)
		}
		return starlark.String(path), nil
	})
	d["check"] = b("check", func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kw []starlark.Tuple) (starlark.Value, error) {
		var tv, rv starlark.Value
		var suite, name, expect, contains string
		expect = "pass"
		if e := starlark.UnpackArgs("check", args, kw, "target", &tv, "suite", &suite, "name", &name, "result", &rv, "expect?", &expect, "contains?", &contains); e != nil {
			return nil, e
		}
		target, e := parseTarget(tv)
		if e != nil {
			return nil, e
		}
		code := field(rv, "exit_code")
		ci, _ := starlark.AsInt32(code)
		output := fieldString(rv, "output")
		actual := fieldString(rv, "status")
		reason := fieldString(rv, "reason")
		status := "pass"
		if expect == "skip" {
			status = "skip"
			reason = "capability not yet implemented"
		} else if expect == "no runner" {
			status = "no runner"
			reason = "no execution backend for this target"
		} else if actual == "no runner" {
			status = "fail"
			reason = "unexpected no runner: " + reason
		} else if expect == "unsupported" {
			if ci == 0 || !strings.Contains(strings.ToLower(output), "unsupported") {
				status = "fail"
				reason = fmt.Sprintf("expected ErrUnsupported, got exit %d (%s)", ci, reason)
			} else {
				status = "skip"
				reason = "expected ErrUnsupported"
			}
		} else if ci != 0 || actual != "pass" {
			status = "fail"
			reason = fmt.Sprintf("exit %d: %s", ci, reason)
		} else if contains != "" && !strings.Contains(output, contains) {
			status = "fail"
			reason = "output missing " + contains
		}
		elapsed, _ := field(rv, "seconds").(starlark.Float)
		h.record(Result{Target: target, Suite: suite, Name: name, Status: status, Output: output, Reason: reason, Seconds: float64(elapsed)})
		return starlark.Bool(status != "fail"), nil
	})
	d["skip"] = b("skip", func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kw []starlark.Tuple) (starlark.Value, error) {
		var tv starlark.Value
		var suite, name, reason string
		if e := starlark.UnpackArgs("skip", args, kw, "target", &tv, "suite", &suite, "name", &name, "reason", &reason); e != nil {
			return nil, e
		}
		target, e := parseTarget(tv)
		if e != nil {
			return nil, e
		}
		status := "skip"
		if strings.HasPrefix(reason, "no runner") {
			status = "no runner"
		}
		h.record(Result{Target: target, Suite: suite, Name: name, Status: status, Reason: reason})
		return starlark.None, nil
	})
	d["assert_eq"] = b("assert_eq", func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kw []starlark.Tuple) (starlark.Value, error) {
		if len(args) != 2 || len(kw) > 0 {
			return nil, fmt.Errorf("assert_eq(a,b)")
		}
		eq, e := starlark.Equal(args[0], args[1])
		if e != nil {
			return nil, e
		}
		if !eq {
			return nil, fmt.Errorf("assert_eq: %s != %s", args[0], args[1])
		}
		return starlark.None, nil
	})
	d["assert_contains"] = b("assert_contains", func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kw []starlark.Tuple) (starlark.Value, error) {
		var hay, needle string
		if e := starlark.UnpackArgs("assert_contains", args, kw, "haystack", &hay, "needle", &needle); e != nil {
			return nil, e
		}
		if !strings.Contains(hay, needle) {
			return nil, fmt.Errorf("missing %q", needle)
		}
		return starlark.None, nil
	})
	d["vm_up"] = b("vm_up", func(t *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kw []starlark.Tuple) (starlark.Value, error) {
		var name string
		if e := starlark.UnpackArgs("vm_up", args, kw, "name", &name); e != nil {
			return nil, e
		}
		b, ok := h.Backend.(*SystemBackend)
		if !ok {
			return starlark.None, nil
		}
		c, cancel := context.WithTimeout(contextOf(t), 30*time.Minute)
		defer cancel()
		if e := b.VMUp(c, name); e != nil {
			return nil, e
		}
		return starlark.None, nil
	})
	d["vm_down"] = b("vm_down", func(t *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kw []starlark.Tuple) (starlark.Value, error) {
		var name string
		if e := starlark.UnpackArgs("vm_down", args, kw, "name", &name); e != nil {
			return nil, e
		}
		b, ok := h.Backend.(*SystemBackend)
		if !ok {
			return starlark.None, nil
		}
		c, cancel := context.WithTimeout(contextOf(t), time.Minute)
		defer cancel()
		if e := b.VMDown(c, name); e != nil {
			return nil, e
		}
		return starlark.None, nil
	})
	return d
}
func runCommand(ctx context.Context, dir string, env, argv []string) ExecResult {
	return runCommandOutput(ctx, dir, env, argv, false)
}

// stdoutOnly is for machine-readable output (go list). Download notices and
// warnings belong to stderr and must not become package names. On failure,
// retain both streams for diagnostics.
func runCommandOutput(ctx context.Context, dir string, env, argv []string, stdoutOnly bool) ExecResult {
	start := time.Now()
	if len(argv) == 0 {
		return ExecResult{ExitCode: -1, Status: "error", Reason: "empty command"}
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.WaitDelay = 5 * time.Second
	var output, diagnostics bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if stdoutOnly {
		cmd.Stderr = &diagnostics
	}
	e := cmd.Run()
	if stdoutOnly && e != nil {
		output.Write(diagnostics.Bytes())
	}
	r := ExecResult{Output: output.String(), Duration: time.Since(start), Status: "pass"}
	if e == exec.ErrWaitDelay && cmd.ProcessState != nil && cmd.ProcessState.Success() {
		return r
	}
	if e != nil {
		r.Status = "fail"
		r.Reason = e.Error()
		r.ExitCode = -1
		if ee, ok := e.(*exec.ExitError); ok {
			r.ExitCode = ee.ExitCode()
		}
	}
	return r
}
func (h *Harness) goCommand(ctx context.Context, t Target, args ...string) ExecResult {
	goexe := filepath.Join("/opt", "go"+t.GoVersion, "bin", "go")
	env := t.Env()
	if _, e := os.Stat(goexe); e != nil {
		goexe = "go"
		// Outside the container, select the requested toolchain explicitly.
		// Never label results with a version that was not used to build them.
		if t.GoVersion != "" {
			env = append(env, "GOTOOLCHAIN=go"+t.GoVersion)
		}
	}
	return runCommandOutput(ctx, h.Root, env, append([]string{goexe}, args...), goListCommand(args))
}

func buildModeSuffix(mode string) string {
	if mode == "" {
		return ""
	}
	return " (" + mode + ")"
}

// nestedModules are the repository's other modules: the backend toolkit and
// independently managed examples. They keep the same cross-build and execution
// coverage as packages in the library module.
var nestedModules = []string{"backend", "examples/snake_termbox", "examples/stackvm"}

func goListCommand(args []string) bool {
	if len(args) >= 2 && args[0] == "-C" {
		args = args[2:]
	}
	return len(args) > 0 && args[0] == "list"
}

func (h *Harness) goPackages(ctx context.Context, t Target) ExecResult {
	args := []string{"list", "-e", "-f", "{{if or .GoFiles .CgoFiles .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}", "./..."}
	result := h.goCommand(ctx, t, args...)
	if result.ExitCode != 0 {
		return result
	}
	for _, module := range nestedModules {
		if _, err := os.Stat(filepath.Join(h.Root, module, "go.mod")); err != nil {
			continue
		}
		next := h.goCommand(ctx, t, append([]string{"-C", module}, args...)...)
		if next.ExitCode != 0 {
			return next
		}
		result.Output += "\n" + next.Output
		result.Duration += next.Duration
	}
	return result
}

func (h *Harness) goPackageCommand(ctx context.Context, t Target, pkg string, args ...string) ExecResult {
	for _, module := range nestedModules {
		path := "github.com/moriyoshi/nyanjit/" + module
		if pkg == path || strings.HasPrefix(pkg, path+"/") {
			args = append([]string{"-C", module}, args...)
			break
		}
	}
	return h.goCommand(ctx, t, append(args, pkg)...)
}

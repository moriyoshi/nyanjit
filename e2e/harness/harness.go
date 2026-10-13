package harness

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"go.starlark.net/starlark"
)

type Filter struct{ Suite, OS, Arch, Target, GoVersion string }
type Target struct{ OS, Arch, GoVersion, GOARM, GOPPC64, GOMIPS64 string }

func (t Target) String() string { return t.OS + "/" + t.Arch }
func (t Target) Key() string {
	variant := strings.Join([]string{t.GOARM, t.GOPPC64, t.GOMIPS64}, "_")
	if variant == "__" {
		variant = "default"
	}
	return t.OS + "_" + t.Arch + "_go" + t.GoVersion + "_" + variant
}
func (t Target) Env() []string {
	return []string{"GOOS=" + t.OS, "GOARCH=" + t.Arch, "GOARM=" + t.GOARM, "GOPPC64=" + t.GOPPC64, "GOMIPS64=" + t.GOMIPS64, "CGO_ENABLED=0"}
}
func (t Target) Runner() string {
	if t.OS == "linux" {
		if t.Arch == "ppc64" {
			// Big-endian ppc64 is unsupported and the image has no
			// binfmt-registered user-mode emulator for it.
			return "no runner"
		}
		if t.Arch == runtime.GOARCH {
			return "native"
		}
		return "qemu-user"
	}
	if t.OS == "windows" {
		if t.Arch == "arm64" && runtime.GOARCH != "arm64" {
			return "no runner"
		}
		return "wine"
	}
	if directory, _, err := vmConfig(vmName(t)); err == nil {
		if _, err := os.Stat(filepath.Join("/opt/nyanjit/vms", directory, "NO-RUNNER")); err == nil {
			return "no runner"
		}
	}
	if t.OS == "openbsd" && (t.Arch == "386" || t.Arch == "arm" || t.Arch == "ppc64") {
		name := "openbsd-" + t.Arch
		if directory, _, err := vmConfig(name); err == nil {
			if _, err := os.Stat(filepath.Join("/opt/nyanjit/vms", directory, "boot.json")); err == nil {
				return "vm"
			}
		}
		return "no runner"
	}
	return "vm"
}
func (t Target) Supported() bool {
	return t.OS == "linux" && (t.Arch == "amd64" || t.Arch == "386" || t.Arch == "arm64" || t.Arch == "riscv64" || t.Arch == "loong64" || t.Arch == "ppc64le")
}

type Result struct {
	Target                              Target
	Suite, Name, Status, Output, Reason string
	Seconds                             float64
}
type ExecResult struct {
	Output   string
	ExitCode int
	Duration time.Duration
	Status   string
	Reason   string
}
type Backend interface {
	Run(context.Context, Target, string, []string, []string) (ExecResult, error)
}
type Harness struct {
	Root    string
	Filter  Filter
	Backend Backend
	sem     chan struct{}
	mu      sync.Mutex
	results []Result
	loaded  map[string]starlark.StringDict
	ctx     context.Context
	script  string
}

func New(root string, jobs int, filter Filter) *Harness {
	if jobs < 1 {
		jobs = 1
	}
	h := &Harness{Root: root, Filter: filter, sem: make(chan struct{}, jobs)}
	h.Backend = &SystemBackend{Root: root}
	return h
}
func (h *Harness) record(r Result) { h.mu.Lock(); h.results = append(h.results, r); h.mu.Unlock() }
func (h *Harness) Results() []Result {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]Result(nil), h.results...)
}
func (h *Harness) selected(t Target, suite string) bool {
	f := h.Filter
	return (f.Suite == "" || f.Suite == suite) && (f.OS == "" || f.OS == t.OS) && (f.Arch == "" || f.Arch == t.Arch) && (f.Target == "" || f.Target == t.String()) && (f.GoVersion == "" || f.GoVersion == t.GoVersion)
}
func (h *Harness) buildRoot() string {
	if info, err := os.Stat("/cache"); err == nil && info.IsDir() {
		return "/cache/build"
	}
	return filepath.Join(h.Root, "e2e", ".build")
}
func (h *Harness) withSlot(ctx context.Context, fn func() (ExecResult, error)) (ExecResult, error) {
	select {
	case h.sem <- struct{}{}:
		defer func() { <-h.sem }()
		return fn()
	case <-ctx.Done():
		return ExecResult{}, ctx.Err()
	}
}
func (h *Harness) Run(ctx context.Context, path string) error {
	if backend, ok := h.Backend.(*SystemBackend); ok {
		defer backend.closeVMs()
	}
	h.ctx = ctx
	h.script = path
	h.loaded = make(map[string]starlark.StringDict)
	thread := h.thread("load", ctx)
	globals, err := starlark.ExecFile(thread, path, nil, h.predeclared())
	if err != nil {
		return fmt.Errorf("load %s: %w", path, err)
	}
	main, ok := globals["main"].(*starlark.Function)
	if !ok {
		return errors.New("script must define main()")
	}
	_, err = h.invoke(ctx, main, nil)
	if err != nil {
		return fmt.Errorf("main(): %w", err)
	}
	for _, r := range h.Results() {
		if r.Status == "fail" {
			return errors.New("E2E matrix has failures")
		}
	}
	return nil
}
func (h *Harness) thread(name string, ctx context.Context) *starlark.Thread {
	t := &starlark.Thread{Name: name, Print: func(_ *starlark.Thread, s string) { fmt.Println(s) }}
	t.SetLocal("ctx", ctx)
	t.Load = func(th *starlark.Thread, module string) (starlark.StringDict, error) {
		if filepath.IsAbs(module) || strings.Contains(module, "..") {
			return nil, fmt.Errorf("invalid load path %q", module)
		}
		path := filepath.Join(filepath.Dir(h.script), module)
		if g, ok := h.loaded[path]; ok {
			return g, nil
		}
		g, err := starlark.ExecFile(th, path, nil, h.predeclared())
		if err == nil {
			h.loaded[path] = g
		}
		return g, err
	}
	return t
}
func (h *Harness) invoke(ctx context.Context, fn starlark.Value, args starlark.Tuple) (starlark.Value, error) {
	t := h.thread("task", ctx)
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			t.Cancel(ctx.Err().Error())
		case <-done:
		}
	}()
	defer close(done)
	return starlark.Call(t, fn, args, nil)
}
func contextOf(t *starlark.Thread) context.Context {
	if c, ok := t.Local("ctx").(context.Context); ok {
		return c
	}
	return context.Background()
}

type xmlCase struct {
	XMLName xml.Name    `xml:"testcase"`
	Name    string      `xml:"name,attr"`
	Class   string      `xml:"classname,attr"`
	Time    string      `xml:"time,attr"`
	Failure *xmlMessage `xml:"failure,omitempty"`
	Skipped *xmlMessage `xml:"skipped,omitempty"`
	Out     string      `xml:"system-out,omitempty"`
}
type xmlMessage struct {
	Message string `xml:"message,attr"`
	Text    string `xml:",chardata"`
}
type xmlSuite struct {
	XMLName  xml.Name  `xml:"testsuite"`
	Name     string    `xml:"name,attr"`
	Tests    int       `xml:"tests,attr"`
	Failures int       `xml:"failures,attr"`
	Skipped  int       `xml:"skipped,attr"`
	Cases    []xmlCase `xml:"testcase"`
}

func (h *Harness) WriteJUnit(path string) error {
	rs := h.Results()
	sort.Slice(rs, func(i, j int) bool {
		return rs[i].Suite+rs[i].Target.Key()+rs[i].Name < rs[j].Suite+rs[j].Target.Key()+rs[j].Name
	})
	s := xmlSuite{Name: "nyanjit-e2e", Tests: len(rs)}
	for _, r := range rs {
		c := xmlCase{Name: r.Name, Class: r.Suite + "." + r.Target.Key(), Time: fmt.Sprintf("%.3f", r.Seconds), Out: r.Output}
		switch r.Status {
		case "fail":
			s.Failures++
			c.Failure = &xmlMessage{Message: r.Reason, Text: r.Output}
		case "skip", "no runner":
			s.Skipped++
			c.Skipped = &xmlMessage{Message: r.Reason}
		}
		s.Cases = append(s.Cases, c)
	}
	b, err := xml.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, append([]byte(xml.Header), b...), 0644)
}
func (h *Harness) PrintSummary(w io.Writer) {
	rs := h.Results()
	sort.Slice(rs, func(i, j int) bool {
		return rs[i].Target.Key()+rs[i].Suite+rs[i].Name < rs[j].Target.Key()+rs[j].Suite+rs[j].Name
	})
	type counts struct {
		target                     Target
		pass, skip, noRunner, fail int
	}
	byTarget := make(map[string]*counts)
	for _, r := range rs {
		c := byTarget[r.Target.Key()]
		if c == nil {
			c = &counts{target: r.Target}
			byTarget[r.Target.Key()] = c
		}
		switch r.Status {
		case "pass":
			c.pass++
		case "skip":
			c.skip++
		case "no runner":
			c.noRunner++
		case "fail":
			c.fail++
		}
	}
	keys := make([]string, 0, len(byTarget))
	for key := range byTarget {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	fmt.Fprintln(w, "TARGET GO VARIANT PASS SKIP NO_RUNNER FAIL")
	for _, key := range keys {
		c := byTarget[key]
		variant := strings.Trim(strings.Join([]string{c.target.GOARM, c.target.GOPPC64, c.target.GOMIPS64}, ","), ",")
		if variant == "" {
			variant = "default"
		}
		fmt.Fprintf(w, "%s %s %s %d %d %d %d\n", c.target.String(), c.target.GoVersion, variant, c.pass, c.skip, c.noRunner, c.fail)
	}
	for _, key := range keys {
		if byTarget[key].target.OS == "solaris" {
			fmt.Fprintln(w, "RUNNER solaris/amd64: Solaris (illumos substitute, OmniOS r151058)")
			break
		}
	}
	seenNoRunner := make(map[string]bool)
	for _, r := range rs {
		if r.Status == "no runner" && !seenNoRunner[r.Target.String()] {
			fmt.Fprintf(w, "NO RUNNER %s: %s\n", r.Target.String(), r.Reason)
			seenNoRunner[r.Target.String()] = true
		}
		if r.Status == "fail" {
			fmt.Fprintf(w, "FAIL %s %s %s: %s\n", r.Target.String(), r.Suite, r.Name, r.Reason)
		}
	}
}

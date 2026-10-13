package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/moriyoshi/nyanjit/e2e/harness"
)

func main() {
	var root, junit, suite, goos, goarch, target, goVersion string
	var jobs int
	var deadline time.Duration
	flag.StringVar(&root, "root", "/src", "repository root")
	flag.StringVar(&junit, "junit", "/cache/results/junit.xml", "JUnit output")
	flag.StringVar(&suite, "suite", "", "suite filter")
	flag.StringVar(&goos, "os", "", "GOOS filter")
	flag.StringVar(&goarch, "arch", "", "GOARCH filter")
	flag.StringVar(&target, "target", "", "GOOS/GOARCH filter")
	flag.StringVar(&goVersion, "go-version", "", "Go version filter (e.g. 1.27.2)")
	flag.IntVar(&jobs, "jobs", 8, "maximum concurrent commands")
	flag.DurationVar(&deadline, "deadline", 8*time.Hour, "whole-suite deadline")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: nyanjit-e2e [flags] e2e/matrix.star")
		os.Exit(2)
	}
	if root == "/src" {
		if _, err := os.Stat(root); err != nil {
			root, _ = filepath.Abs("..")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	h := harness.New(root, jobs, harness.Filter{Suite: suite, OS: goos, Arch: goarch, Target: strings.TrimSpace(target), GoVersion: goVersion})
	path := flag.Arg(0)
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	err := h.Run(ctx, path)
	if werr := h.WriteJUnit(junit); werr != nil {
		fmt.Fprintln(os.Stderr, werr)
		if err == nil {
			err = werr
		}
	}
	h.PrintSummary(os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

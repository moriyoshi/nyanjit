package main

import (
	"bufio"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestExternalBaseImagesUseDigests(t *testing.T) {
	f, err := os.Open("Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	pinned := regexp.MustCompile(`^[^@]+@sha256:[0-9a-f]{64}$`)
	stages := map[string]bool{}
	scanner := bufio.NewScanner(f)
	external := 0
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 || fields[0] != "FROM" {
			continue
		}
		fields = fields[1:]
		if strings.HasPrefix(fields[0], "--platform=") {
			fields = fields[1:]
		}
		if !stages[fields[0]] {
			external++
			if !pinned.MatchString(fields[0]) {
				t.Errorf("external image is mutable: %s", fields[0])
			}
		}
		if len(fields) == 3 && fields[1] == "AS" {
			stages[fields[2]] = true
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if external == 0 {
		t.Fatal("no external base images checked")
	}
}

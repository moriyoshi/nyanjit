//go:build linux && (arm64 || amd64 || 386 || riscv64 || ppc64 || ppc64le)

package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunGameMovesAndQuits(t *testing.T) {
	board, _, head, _ := initialBoard()
	var output bytes.Buffer
	if err := runGame(strings.NewReader("d\nq\n"), &output); err != nil {
		t.Fatal(err)
	}
	want := append([]byte(nil), board...)
	want[head-2] = ' '
	want[head] = 'o'
	want[head+1] = '@'
	if got := output.Bytes(); !bytes.Equal(got, append(board, want...)) {
		t.Fatalf("game output did not show the initial board followed by one step")
	}
}

func TestRunGameReportsCollision(t *testing.T) {
	var output bytes.Buffer
	if err := runGame(strings.NewReader(strings.Repeat("d\n", 8)), &output); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(output.Bytes(), []byte("\nGame over!\n")) {
		t.Fatalf("game did not report wall collision")
	}
}

func TestRunGameTurnsAndEatsFood(t *testing.T) {
	board, _, _, food := initialBoard()
	var output bytes.Buffer
	if err := runGame(strings.NewReader("d\nd\nw\nw\nq\n"), &output); err != nil {
		t.Fatal(err)
	}
	if got := output.Bytes(); len(got) != 5*len(board) {
		t.Fatalf("got %d output bytes, want %d", len(got), 5*len(board))
	} else if got[4*len(board)+food] != '@' {
		t.Fatalf("snake did not eat the initial food")
	}
}

func TestRunGameRejectsReverseDirection(t *testing.T) {
	board, _, head, _ := initialBoard()
	var output bytes.Buffer
	if err := runGame(strings.NewReader("a\nq\n"), &output); err != nil {
		t.Fatal(err)
	}
	want := append([]byte(nil), board...)
	want[head-2] = ' '
	want[head] = 'o'
	want[head+1] = '@'
	if got := output.Bytes(); !bytes.Equal(got, append(board, want...)) {
		t.Fatal("reverse input changed the snake's direction")
	}
}

package main

import (
	"fmt"
	"log"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
)

func main() {
	b := nyanjit.NewBuilder()
	x := b.ParamNative()

	// Values from the enclosing scope remain usable inside this scope.
	b.Enter()
	shifted := b.Add(x, b.ConstNative(2))
	scaled := b.Mul(shifted, b.ConstNative(3))
	// Export scaled to the enclosing scope; shifted and the constants expire.
	b.Leave(scaled)

	// Both the original parameter and the exported value remain usable.
	b.Return(b.Add(scaled, x)) // (x + 2) * 3 + x
	program, err := b.Finish()
	if err != nil {
		log.Fatal(err)
	}

	code, err := nyanjit.Compile(program, compile.TargetSpec{}, compile.Func{ABI: abi.C})
	if err != nil {
		log.Fatal(err)
	}
	defer code.Close()

	result, err := code.Call(9)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result) // 42
}

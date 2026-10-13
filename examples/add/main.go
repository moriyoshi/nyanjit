package main

import (
	"fmt"
	"log"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/compile"
)

func main() {
	b := nyanjit.NewBuilder()
	b.Return(b.Add(b.ParamNative(), b.ParamNative()))
	program, err := b.Finish()
	if err != nil {
		log.Fatal(err)
	}
	code, err := compile.Compile(program, nyanjit.CompileOptions{ABI: nyanjit.ABIC})
	if err != nil {
		log.Fatal(err)
	}
	defer code.Close()

	result, err := code.Call(20, 22)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result)
}

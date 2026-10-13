module github.com/moriyoshi/nyanjit/examples/snake_termbox

go 1.26.0

require (
	github.com/moriyoshi/nyanjit v0.0.0
	github.com/moriyoshi/nyanjit/backend v0.0.0
	github.com/nsf/termbox-go v1.1.2
)

require (
	github.com/clipperhouse/uax29/v2 v2.2.0 // indirect
	github.com/ebitengine/purego v0.12.0-alpha.1.0.20261009162626-eafd1cca3a52 // indirect
	github.com/mattn/go-runewidth v0.0.19 // indirect
	golang.org/x/sys v0.31.0 // indirect
)

replace (
	github.com/moriyoshi/nyanjit => ../..
	github.com/moriyoshi/nyanjit/backend => ../../backend
)

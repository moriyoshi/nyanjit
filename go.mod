module github.com/moriyoshi/nyanjit

go 1.26.0

require (
	github.com/ebitengine/purego v0.12.0-alpha.1.0.20261009162626-eafd1cca3a52
	github.com/moriyoshi/nyanjit/backend v0.0.0
)

require golang.org/x/sys v0.31.0 // indirect

// The toolkit module lives in this repository and is unpublished.
replace github.com/moriyoshi/nyanjit/backend => ./backend

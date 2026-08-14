module github.com/daknoblo/forecast-tool

// Pinned to the patch level: the workflows install this exact toolchain via
// go-version-file, and 1.26.6 carries the current standard-library fixes.
go 1.26.6

require (
	github.com/rickar/cal/v2 v2.1.29
	github.com/yuin/goldmark v1.8.5
)

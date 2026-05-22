module github.com/arribada/marlin-core/databrowser

go 1.24

toolchain go1.24.0

require github.com/arribada/marlin-common v0.0.0-00010101000000-000000000000

require (
	github.com/davecgh/go-spew v1.1.1
	github.com/mpl/basicauth v0.0.0-20250721104714-3b2305e49960
)

replace github.com/arribada/marlin-common => ../marlin-common

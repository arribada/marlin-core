module github.com/arribada/marlin-core/gc

go 1.23.0

toolchain go1.24.0

require (
	github.com/Azure/azure-sdk-for-go/sdk/storage/azfile v1.5.1
	github.com/arribada/marlin-core/common v0.1.0
	golang.org/x/sys v0.34.0
)

require (
	github.com/Azure/azure-sdk-for-go/sdk/azcore v1.18.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/internal v1.11.1 // indirect
	golang.org/x/net v0.38.0 // indirect
	golang.org/x/text v0.23.0 // indirect
)

replace github.com/arribada/marlin-core/common => ../common

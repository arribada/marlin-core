module github.com/arribada/marlin-core/collector

go 1.22

require (
	github.com/Azure/azure-sdk-for-go/sdk/storage/azfile v1.3.0
	github.com/adrianmo/go-nmea v1.10.0
	github.com/arribada/marlin-core/common v0.1.0
	github.com/fsnotify/fsnotify v1.7.0
	github.com/go-kit/kit v0.10.0
	github.com/mpl/basicauth v0.0.0-20250721104714-3b2305e49960
	github.com/nareix/joy5 v0.0.0-00010101000000-000000000000
	github.com/oklog/run v1.1.1-0.20200508094559-c7096881717e
	github.com/pkg/errors v0.9.1
	go.bug.st/serial v1.6.2
)

require github.com/creack/goselect v0.1.2 // indirect

require (
	github.com/Azure/azure-sdk-for-go/sdk/azcore v1.13.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/internal v1.10.0 // indirect
	github.com/go-logfmt/logfmt v0.5.0 // indirect
	golang.org/x/net v0.27.0 // indirect
	golang.org/x/sys v0.22.0
	golang.org/x/text v0.16.0 // indirect
)

replace (
	github.com/arribada/marlin-core/common => ../common
	github.com/grandcat/zeroconf => github.com/arribada/zeroconf v1.0.1-0.20210324085928-a9974948b214
	github.com/nareix/joy5 => github.com/arribada/joy5 v0.0.0-20210113093624-f5728ad0ac71
)

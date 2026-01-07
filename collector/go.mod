module github.com/arribada/marlin-core/collector

go 1.24.0

require (
	github.com/Azure/azure-sdk-for-go/sdk/storage/azfile v1.3.0
	github.com/adrianmo/go-nmea v1.10.0
	github.com/arribada/marlin-core/common v0.1.0
	github.com/bluenviron/gortsplib/v5 v5.2.2
	github.com/bluenviron/mediacommon/v2 v2.6.0
	github.com/fsnotify/fsnotify v1.7.0
	github.com/go-kit/kit v0.10.0
	github.com/mpl/basicauth v0.0.0-20250721104714-3b2305e49960
	github.com/oklog/run v1.1.1-0.20200508094559-c7096881717e
	github.com/pion/rtp v1.10.0
	github.com/pkg/errors v0.9.1
	go.bug.st/serial v1.6.2
)

require (
	github.com/asticode/go-astikit v0.30.0 // indirect
	github.com/asticode/go-astits v1.14.0 // indirect
	github.com/creack/goselect v0.1.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/pion/logging v0.2.4 // indirect
	github.com/pion/randutil v0.1.0 // indirect
	github.com/pion/rtcp v1.2.16 // indirect
	github.com/pion/sdp/v3 v3.0.17 // indirect
	github.com/pion/srtp/v3 v3.0.9 // indirect
	github.com/pion/transport/v3 v3.1.1 // indirect
)

require (
	github.com/Azure/azure-sdk-for-go/sdk/azcore v1.13.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/internal v1.10.0 // indirect
	github.com/go-logfmt/logfmt v0.5.0 // indirect
	golang.org/x/net v0.48.0 // indirect
	golang.org/x/sys v0.39.0
	golang.org/x/text v0.32.0 // indirect
)

replace (
	github.com/arribada/marlin-core/common => ../common
	github.com/grandcat/zeroconf => github.com/arribada/zeroconf v1.0.1-0.20210324085928-a9974948b214
	github.com/nareix/joy5 => github.com/arribada/joy5 v0.0.0-20210113093624-f5728ad0ac71
)

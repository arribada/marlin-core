module github.com/arribada/marlin-core/collector

go 1.22

require (
	github.com/Azure/azure-sdk-for-go/sdk/storage/azfile v1.3.0
	github.com/adrianmo/go-nmea v1.10.0
	github.com/arribada/marlin-core/common v0.1.0
	github.com/fsnotify/fsnotify v1.7.0
	github.com/go-kit/kit v0.10.0
	github.com/nareix/joy5 v0.0.0-00010101000000-000000000000
	github.com/oklog/run v1.1.1-0.20200508094559-c7096881717e
	github.com/pkg/errors v0.9.1
	go-hep.org/x/hep v0.35.0
	go.bug.st/serial v1.6.2
	gonum.org/v1/plot v0.14.0
)

require (
	git.sr.ht/~sbinet/gg v0.5.0 // indirect
	github.com/ajstarks/svgo v0.0.0-20211024235047-1546f124cd8b // indirect
	github.com/campoy/embedmd v1.0.0 // indirect
	github.com/creack/goselect v0.1.2 // indirect
	github.com/go-fonts/liberation v0.3.2 // indirect
	github.com/go-latex/latex v0.0.0-20231108140139-5c1ce85aa4ea // indirect
	github.com/go-pdf/fpdf v0.9.0 // indirect
	github.com/golang/freetype v0.0.0-20170609003504-e2365dfdc4a0 // indirect
	github.com/gonuts/binary v0.2.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	golang.org/x/exp v0.0.0-20240613232115-7f521ea00fb8 // indirect
	golang.org/x/image v0.17.0 // indirect
	golang.org/x/sync v0.7.0 // indirect
	gonum.org/v1/gonum v0.15.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

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

module github.com/arribada/marlin-core/collector

go 1.22

require (
	github.com/Azure/azure-sdk-for-go/sdk/storage/azfile v1.3.0
	github.com/adrianmo/go-nmea v1.10.0
	github.com/arribada/marlin-core/common v0.1.0
	github.com/davecgh/go-spew v1.1.1
	github.com/fsnotify/fsnotify v1.7.0
	github.com/go-audio/audio v1.0.0
	github.com/go-audio/wav v1.0.0
	github.com/go-kit/kit v0.10.0
	github.com/golang/protobuf v1.4.3
	github.com/grandcat/zeroconf v0.0.0-00010101000000-000000000000
	github.com/miekg/dns v1.1.27
	github.com/nareix/joy5 v0.0.0-00010101000000-000000000000
	github.com/oklog/run v1.1.1-0.20200508094559-c7096881717e
	github.com/pkg/errors v0.9.1
	github.com/pmezard/go-difflib v1.0.0
	github.com/prometheus/client_golang v1.3.0
	github.com/prometheus/client_model v0.1.0
	github.com/stretchr/testify v1.9.0
	github.com/warthog618/gpiod v0.6.1-0.20210121142103-4eb47bd10333
	go-hep.org/x/hep v0.35.0
	go.bug.st/serial v1.6.2
	go.uber.org/goleak v1.1.10
	go.uber.org/multierr v1.3.0
	gonum.org/v1/plot v0.14.0
	google.golang.org/grpc v1.36.0-dev.0.20210122012134-2c42474aca0c
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
	golang.org/x/exp v0.0.0-20240613232115-7f521ea00fb8 // indirect
	golang.org/x/image v0.17.0 // indirect
	golang.org/x/mod v0.18.0 // indirect
	golang.org/x/sync v0.7.0 // indirect
	gonum.org/v1/gonum v0.15.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

require (
	github.com/Azure/azure-sdk-for-go/sdk/azcore v1.13.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/internal v1.10.0 // indirect
	github.com/BurntSushi/toml v0.3.1 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.1.1 // indirect
	github.com/go-audio/riff v1.0.0 // indirect
	github.com/go-logfmt/logfmt v0.5.0 // indirect
	github.com/kelseyhightower/envconfig v1.4.0
	github.com/matttproud/golang_protobuf_extensions v1.0.1 // indirect
	github.com/prometheus/common v0.7.0 // indirect
	github.com/prometheus/procfs v0.0.8 // indirect
	github.com/uber-go/multierr v1.1.0 // indirect
	go.uber.org/atomic v1.7.0 // indirect
	go.uber.org/tools v0.0.0-20190618225709-2cfd321de3ee // indirect
	golang.org/x/crypto v0.25.0 // indirect
	golang.org/x/lint v0.0.0-20190930215403-16217165b5de // indirect
	golang.org/x/net v0.27.0 // indirect
	golang.org/x/sys v0.22.0
	golang.org/x/text v0.16.0 // indirect
	golang.org/x/tools v0.22.0 // indirect
	google.golang.org/genproto v0.0.0-20200526211855-cb27e3aa2013 // indirect
	google.golang.org/protobuf v1.25.0 // indirect
	honnef.co/go/tools v0.1.3 // indirect
)

replace (
	github.com/arribada/marlin-core/common => ../common
	github.com/grandcat/zeroconf => github.com/arribada/zeroconf v1.0.1-0.20210324085928-a9974948b214
	github.com/nareix/joy5 => github.com/arribada/joy5 v0.0.0-20210113093624-f5728ad0ac71
)

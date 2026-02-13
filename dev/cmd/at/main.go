package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/warthog618/modem/at"
	"github.com/warthog618/modem/serial"
	"github.com/warthog618/modem/trace"
	// "go.bug.st/serial"
	// "go.bug.st/serial/enumerator"
)

var version = "undefined"

var (
	flagDuration int
	timeout      time.Duration
)

func init() {
	flag.IntVar(&flagDuration, "timeout", 500, "command timeout period in milliseconds")
}

func main() {
	dev := flag.String("d", "/dev/ttyUSB2", "path to modem device")
	baud := flag.Int("b", 115200, "baud rate")
	verbose := flag.Bool("v", false, "log modem interactions")
	vsn := flag.Bool("version", false, "report version and exit")
	theCMD := flag.String("cmd", "", "AT command to send")

	flag.Parse()
	timeout = time.Duration(flagDuration) * time.Millisecond
	if *vsn {
		fmt.Printf("%s %s\n", os.Args[0], version)
		os.Exit(0)
	}
	m, err := serial.New(serial.WithPort(*dev), serial.WithBaud(*baud))
	if err != nil {
		log.Fatal(err)
	}
	defer m.Close()
	var mio io.ReadWriter = m
	if *verbose {
		mio = trace.New(m)
	}
	a := at.New(mio, at.WithTimeout(timeout))
	err = a.Init()
	if err != nil {
		log.Fatal(err)
	}

	if *theCMD == "" {
		return
	}

	if *verbose {
		info, err := a.Command("E1")
		if err != nil {
			log.Printf(" %s\n", err)
		}
		for _, l := range info {
			fmt.Printf(" %s\n", l)
		}
	}

	info, err := a.Command(*theCMD)
	if err != nil {
		log.Printf(" %s\n", err)
	}
	for _, l := range info {
		fmt.Printf(" %s\n", l)
	}
}

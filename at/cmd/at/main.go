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

var ()

func main() {
	/*
		ports, err := serial.GetPortsList()
		if err != nil {
			log.Fatal(err)
		}
		if len(ports) == 0 {
			log.Fatal("No serial ports found!")
		}
		for _, port := range ports {
			fmt.Printf("Found port: %v\n", port)
		}

		prts, err := enumerator.GetDetailedPortsList()
		if err != nil {
			log.Fatal(err)
		}
		if len(prts) == 0 {
			fmt.Println("No serial ports found!")
			return
		}
		for _, port := range prts {
			fmt.Printf("Found port: %v\n", port)
			if port.IsUSB {
				fmt.Printf("   USB ID     %s:%s\n", port.VID, port.PID)
				fmt.Printf("   USB serial %s\n", port.SerialNumber)
			}
		}
	*/

	dev := flag.String("d", "/dev/ttyUSB2", "path to modem device")
	baud := flag.Int("b", 115200, "baud rate")
	timeout := flag.Duration("t", 400*time.Millisecond, "command timeout period")
	verbose := flag.Bool("v", false, "log modem interactions")
	vsn := flag.Bool("version", false, "report version and exit")
	theCMD := flag.String("cmd", "", "AT command to send")

	flag.Parse()
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
	a := at.New(mio, at.WithTimeout(*timeout))
	err = a.Init()
	if err != nil {
		log.Fatal(err)
	}

	/*
		cmds := []string{
			"I",
			"+GCAP",
			"+CMEE=2",
			"+CGMI",
			"+CGMM",
			"+CGMR",
			"+CGSN",
			"+CSQ",
			"+CIMI",
			"+CREG?",
			"+CNUM",
			"+CPIN?",
			"+CEER",
			"+CSCA?",
			"+CSMS?",
			"+CSMS=?",
			"+CPMS=?",
			"+CCID?",
			"+CCID=?",
			"^ICCID?",
			"+CNMI?",
			"+CNMI=?",
			"+CNMA=?",
			"+CMGF?",
			"+CMGF=?",
			"+CUSD?",
			"+CUSD=?",
			"^USSDMODE?",
			"^USSDMODE=?",
		}
			for _, cmd := range cmds {
				info, err := a.Command(cmd)
				fmt.Println("AT" + cmd)
				if err != nil {
					fmt.Printf(" %s\n", err)
					continue
				}
				for _, l := range info {
					fmt.Printf(" %s\n", l)
				}
			}
	*/

	if *theCMD == "" {
		return
	}

	info, err := a.Command(*theCMD)
	if err != nil {
		log.Printf(" %s\n", err)
	}
	for _, l := range info {
		fmt.Printf(" %s\n", l)
	}
}

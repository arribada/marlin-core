package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
)

oteuhotneu

var (
	flagLocalAddr string
	flagDest      string
	flagVerbose   bool
)

func init() {
	flag.StringVar(&flagLocalAddr, "local", "", "local address")
	flag.StringVar(&flagDest, "dest", "", "file to fetch")
	flag.BoolVar(&flagVerbose, "v", false, "be verbose")
}

func main() {
	flag.Parse()
	if flagDest == "" {
		log.Fatal("-dest is not set")
	}
	if flagLocalAddr == "" {
		log.Fatal("-local is not set")
	}

	addr, err := net.ResolveTCPAddr("tcp", flagLocalAddr+":0")
	if err != nil {
		log.Fatal(err)
	}

	// TODO: this is actually not enough to mimic e.g. ping -I wwan0
	// as we're still dependent on whatever the default route(s) are.
	// See https://github.com/gokrazy/gokrazy/issues/328#issuecomment-3558968557
	dialer := &net.Dialer{LocalAddr: addr}

	dialContext := func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := dialer.Dial(network, addr)
		return conn, err
	}

	transport := &http.Transport{DialContext: dialContext}
	client := &http.Client{
		Transport: transport,
	}

	// http request
	response, err := client.Get(flagDest)
	if err != nil {
		log.Fatal(err)
	}

	data, err := ioutil.ReadAll(response.Body)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(string(data))
}

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"cloud.google.com/go/logging"
)

var (
	glog *logging.Logger

	flagGoogCredentials string
)

func init() {
	flag.StringVar(&flagGoogCredentials, "goog_creds", "", "google application credentials file. leave empty to disable goog logging.")
}

func initGoogLogging() *logging.Client {
	if flagGoogCredentials == "" {
		log.Fatal("no google application credentials file provided")
	}
	if err := os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", flagGoogCredentials); err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	client, err := logging.NewClient(ctx, "nimble-crane-448814-k5")
	if err != nil {
		log.Fatal(err)
	}
	glog = client.Logger("marlintest")
	return client
}

type MyEntry struct {
	Hostname string
	Tag      string
	Content  string
}

func main() {
	flag.Parse()
	initGoogLogging()

	count := 0
	for {
		glog.Log(logging.Entry{Payload: MyEntry{
			Hostname: "mbp.mathieu",
			Tag:      "GoogCloudLoggingTesting",
			Content:  fmt.Sprintf("COUNT: %d", count),
		}})
		time.Sleep(10 * time.Second)
		count++
	}
}

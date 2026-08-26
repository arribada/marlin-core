// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package main

import (
	"log"
	"net/http"
)

func main() {
	res, err := http.Post("http://localhost:8080/reset", "", nil)
	if err != nil {
		log.Fatal(err)
	}
	if res.StatusCode != 200 {
		log.Fatalf("%v", res.StatusCode)
	}
}

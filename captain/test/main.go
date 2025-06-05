package main

import (
	"log"

	"github.com/arribada/insight-360/common/pkg/utils"
)

func main() {
	if err := utils.WaitForDrive("localhost:1234"); err != nil {
		log.Fatal(err)
	}
	println("CAPTAIN READY")
}

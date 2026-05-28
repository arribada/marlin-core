// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package main

import (
	"context"
	"log"

	"github.com/arribada/marlin-core/collector/pkg/lpm/fake"
)

func main() {
	if err := fake.Run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package main

import (
	"github.com/arribada/marlin-core/collector/pkg/collector"
	"github.com/arribada/marlin-core/collector/pkg/config"
)

func main() {
	cfg, err := config.Parse()
	if err != nil {
		collector.Logger.Fatalf("while loading configuration: %v", err)
	}
	if err := collector.Collect(cfg); err != nil {
		collector.Logger.Fatal(err)
	}
}

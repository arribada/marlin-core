// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package lpm

import (
	"fmt"
	"log"

	"github.com/arribada/marlin-common/pkg/sat"
)

func AlertToBytes(alert sat.Alert) ([]byte, error) {
	return sat.GenRawData(alert.Latitude, alert.Longitude, alert.Time, sat.LDA2Size)
}

func SendAlert(alert sat.Alert) error {
	log.Printf("Sending Alert for position (%f, %f) at %v", alert.Latitude, alert.Longitude, alert.Time)

	data, err := AlertToBytes(alert)
	if err != nil {
		return fmt.Errorf("while packing alert as bytes stream: %v", err)
	}

	if err := sendTX(data); err != nil {
		return fmt.Errorf("while sending alert: %v", err)
	}
	return nil
}

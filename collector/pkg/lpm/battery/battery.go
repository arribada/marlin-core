// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package battery

import (
	"errors"
	"log"
	"sync"
)

var (
	// TODO: logger for it. We shouldn't even be logging from within here anyway, but deal with that later.
	Verbose bool
	// Voltage value at which we consider we are low on charge, and we
	// should shut down everything cleanly.
	LowVoltage           = 10.
	LowVoltageErr        = errors.New("battery on low voltage")
	DischargingThreshold = -0.02
	// When running on battery power (battery is discharging), we stop
	// most activity (esp video capture), to prioritize data uploading.
	// It is ok to assume that" battery discharging <==> vessel in in
	// harbour", because it is very unlikely that the vessel's power will
	// be shut off while at sea.
	OnBatteryErr = errors.New("running on battery")
	OnMainsErr   = errors.New("running on mains")

	Mu      sync.RWMutex
	Voltage float64
	// Current < DischargingThreshold means battery is discharging
	// (and we should switch to low activity mode)
	Current float64
)

func IsDischarging() bool {
	Mu.RLock()
	defer Mu.RUnlock()
	if Verbose {
		log.Printf("IsDischarging: (%v, %v) %v", Current, Voltage, Current < DischargingThreshold)
	}
	return Current < DischargingThreshold
}

// IsLowVoltage returns whether the battery is discharging and is
// below the LowVoltage threshold.
func IsLowVoltage() bool {
	Mu.RLock()
	defer Mu.RUnlock()
	if Verbose {
		log.Printf("IsLowVoltage: (%v, %v) %v", Current, Voltage, Current < DischargingThreshold && Voltage < LowVoltage)
	}
	return Current < DischargingThreshold && Voltage < LowVoltage
}

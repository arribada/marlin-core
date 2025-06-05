// Copyright (c) The Arribada initiative.
// Licensed under the MIT License.

package tests

import (
	"github.com/adrianmo/go-nmea"
	"github.com/arribada/marlin-core/collector/pkg/node/gpsr"
)

var SRMCp = []gpsr.SRMCplus{
	{
		HDOP: 0.1,
		SRMC: gpsr.SRMC{
			Time: nmea.Time{
				Hour:        16,
				Minute:      19,
				Second:      19,
				Millisecond: 123,
			},
			Latitude:  45.411735,
			Longitude: 3.181687,
			Course:    12.3,
			Speed:     1.245,
			Date: nmea.Date{
				DD: 28,
				MM: 06,
				YY: 24,
			},
		},
	},
	{
		HDOP: 0.2,
		SRMC: gpsr.SRMC{
			Time: nmea.Time{
				Hour:        17,
				Minute:      19,
				Second:      19,
				Millisecond: 123,
			},
			Latitude:  45.411735,
			Longitude: 3.181687,
			Course:    23.4,
			Speed:     1.145,
			Date: nmea.Date{
				DD: 28,
				MM: 06,
				YY: 24,
			},
		},
	},
	// we're "fully stopped" now.
	{
		HDOP: 0.3,
		SRMC: gpsr.SRMC{
			Time: nmea.Time{
				Hour:        18,
				Minute:      19,
				Second:      19,
				Millisecond: 234,
			},
			Latitude:  45.411735,
			Longitude: 3.181687,
			Course:    34.5,
			Speed:     0.40,
			Date: nmea.Date{
				DD: 28,
				MM: 06,
				YY: 24,
			},
		},
	},
	// We've started moving again, but index is still somewhat high.
	{
		HDOP: 0.4,
		SRMC: gpsr.SRMC{
			Time: nmea.Time{
				Hour:        19,
				Minute:      19,
				Second:      19,
				Millisecond: 345,
			},
			Latitude:  45.411735,
			Longitude: 3.181687,
			Course:    45.6,
			Speed:     0.745,
			Date: nmea.Date{
				DD: 28,
				MM: 06,
				YY: 24,
			},
		},
	},
	{
		HDOP: 0.5,
		SRMC: gpsr.SRMC{
			Time: nmea.Time{
				Hour:        20,
				Minute:      19,
				Second:      19,
				Millisecond: 345,
			},
			Latitude:  45.411735,
			Longitude: 3.181687,
			Course:    56.7,
			Speed:     1.245,
			Date: nmea.Date{
				DD: 28,
				MM: 06,
				YY: 24,
			},
		},
	},
}

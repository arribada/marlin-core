// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package populate

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azfile/fileerror"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azfile/share"
)

var rootDir = "/perm/tests/"

var (
	FlagPopulate   string
	FlagVessel     string
	FlagConnString string
)

func makeData(size int) ([]byte, error) {
	max := big.NewInt(255)
	data := make([]byte, size)
	for i := range data {
		val, err := rand.Int(rand.Reader, max)
		if err != nil {
			return nil, err
		}
		v := byte(val.Int64())
		data[i] = v
	}
	return data, nil
}

func initVid() ([]byte, error) {
	// 2GB spread on 1 year. 23 videos each day.
	// -> ~240K per vid
	// Maybe add a bit of randomization on that number later.
	size := int(2e9) / 365
	size = int(size / 23)
	println("video size: ", size)
	return makeData(size)
}

func initGPS() ([]byte, error) {
	return makeData(1e3)
}

// TODO: make sure we are compliant with the new upload system (i.e. chronological only)
func Populate(upload bool, skipSome bool) error {
	var azShareCl *share.Client
	if upload {
		var err error
		azShareCl, err = share.NewClientFromConnectionString(FlagConnString, FlagVessel, nil)
		if err != nil {
			return err
		}
		if _, err := azShareCl.Create(context.TODO(), nil); err != nil {
			if !fileerror.HasCode(err, fileerror.ShareAlreadyExists) {
				return fmt.Errorf("Azure share creation failed: %v", err)
			}
			// already exists, all good
		}
	}
	vidData, err := initVid()
	if err != nil {
		return err
	}

	gpsData, err := initGPS()
	if err != nil {
		return err
	}

	day, err := time.Parse(time.DateOnly, "2025-01-01")
	if err != nil {
		return err
	}
	endDay, err := time.Parse(time.DateOnly, "2025-12-31")
	if err != nil {
		return err
	}

	for {
		if day.After(endDay) {
			break
		}
		dayName := day.Format(time.DateOnly)
		dayDir := filepath.Join(FlagPopulate, dayName)
		// TODO: use common.DataTypes
		for _, dt := range []string{"video", "gps"} {
			dtDir := filepath.Join(dayDir, dt)
			if err := os.MkdirAll(dtDir, 0755); err != nil {
				return err
			}
			t := day.Add(time.Second)
			for i := 0; i < 23; i++ {
				name := t.Format(time.RFC3339)
				name = strings.Replace(name, ":", "_", -1)
				name = dt + "-" + name
				var data []byte
				if dt == "gps" {
					name = name + ".json"
					data = gpsData
				}
				if dt == "video" {
					name = name + ".mp4"
					data = vidData
				}
				fullPath := filepath.Join(dtDir, name)
				if _, err := os.Stat(fullPath); err != nil {
					if !os.IsNotExist(err) {
						return err
					}
					if err := os.WriteFile(fullPath, data, 0755); err != nil {
						return err
					}
				}
				t = t.Add(time.Hour)
				if !upload {
					continue
				}
				if err := syncToAzure(azShareCl, fullPath); err != nil {
					return err
				}
			}
		}
		day = day.Add(24 * time.Hour)
	}
	return nil
}

// TODO: code replicated from collector. Refactor it in a common place.
func syncToAzure(azShareCl *share.Client, fullPath string) error {
	parts := strings.Split(fullPath, string(filepath.Separator))
	lp := len(parts)
	if lp < 3 {
		return fmt.Errorf("unexpected path hierarchy, wanted 'someprefix/datetime/datatype/filename', got %s", fullPath)
	}

	datetime, datatype, filename := parts[lp-3], parts[lp-2], parts[lp-1]

	datetimeCl := azShareCl.NewDirectoryClient(datetime)
	if _, err := datetimeCl.Create(context.TODO(), nil); err != nil {
		if !fileerror.HasCode(err, fileerror.ResourceAlreadyExists) {
			return fmt.Errorf("failed to create datetime directory %v: %v", datetime, err)
		}
	}

	datatypeCl := datetimeCl.NewSubdirectoryClient(datatype)
	if _, err := datatypeCl.Create(context.TODO(), nil); err != nil {
		if !fileerror.HasCode(err, fileerror.ResourceAlreadyExists) {
			return fmt.Errorf("failed to create datatype directory %v: %v", datatype, err)
		}
	}

	f, err := os.OpenFile(fullPath, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	fInfo, err := f.Stat()
	if err != nil {
		return err
	}
	fSize := fInfo.Size()

	fClient := datatypeCl.NewFileClient(filename)
	if _, err := fClient.GetProperties(context.TODO(), nil); err == nil {
		// already exists, all good
		return nil
	}
	if _, err := fClient.Create(context.TODO(), fSize, nil); err != nil {
		if fileerror.HasCode(err, fileerror.ResourceAlreadyExists) {
			// already exists, all good
			// TODO: that actually does not happen/work with file creation unfortunately,
			// which is why we do the GetProperties check above instead.
			return nil
		}
		return fmt.Errorf("failed to create file %v: %v", filename, err)
	}

	if err := fClient.UploadFile(context.TODO(), f, nil); err != nil {
		return fmt.Errorf("failed to upload contents for file %v: %v", filename, err)
	}

	log.Printf("Successfully uploaded to Azure: %v", fullPath)
	return nil
}

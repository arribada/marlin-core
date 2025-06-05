// Copyright (c) The Arribada initiative.
// Licensed under the MIT License.

package utils

/*
import (
	"os"
	"fmt"
	"log"

	diskfs "github.com/diskfs/go-diskfs"
	"github.com/diskfs/go-diskfs/disk"
	"github.com/diskfs/go-diskfs/backend/file"
	"github.com/diskfs/go-diskfs/filesystem"
	"github.com/diskfs/go-diskfs/partition/gpt"
)

const (
	theLabel = "EXTDIR1"
	theDevice = "/dev/sda"
	partName = "marlin_data"
)

func main() {
	err, critical := getLabel(theLabel, theDevice)
	if err == nil {
		// nothing to do
		println("ALL GOOD")
		os.Exit(0)
	}

	if critical {
		// in gokrazy we won't want that, as there is no point to restart the service in that case
		log.Fatal(err)
	}
	
	if err := PrepSSD(theLabel, theDevice); err != nil {
		// in gokrazy we won't want that, as there is no point to restart the service in that case
		log.Fatal(err)
	}
}

func getLabel(label, device string) (err error, critical bool) {
	theBackend, err := file.OpenFromPath(device, true)
	if err != nil {
		return err, true
	}

	theDisk, err := diskfs.OpenBackend(theBackend)
	if err != nil {
		return err, true
	}

	fs, err := theDisk.GetFilesystem(1)
	if err != nil {
		return err, false
	}
	
	lbl := fs.Label()
	if lbl != label {
		return fmt.Errorf("label %v not found (%v found instead)", label, lbl), false
	}
		
	return nil, false
}

func PrepSSD(label, device string) error {
	theBackend, err := file.OpenFromPath(device, false)
	if err != nil {
		return err
	}

	theDisk, err := diskfs.OpenBackend(theBackend)
	if err != nil {
		return err
	}

	println("DISK SIZE: ", theDisk.Size)
	println("DISK LOG SIZE: ", theDisk.LogicalBlocksize)
	println("DISK PHYS SIZE: ", theDisk.PhysicalBlocksize)

	sectorSize := 512
	startSector := 40 // 34 really, because GPT headers are 33 sectors, but nicely aligned at 8 factor for performance
	sizeInSectors := uint64(theDisk.Size) / uint64(sectorSize)
	sizeInSectors -= uint64(startSector) // startSector
	sizeInSectors -= 33 // leave space for backup GPT at the end

	// partition
	part := gpt.Partition{
		Start: uint64(startSector),
		// End        uint64 // end sector for the partition
		Size: sizeInSectors * uint64(sectorSize),
		Type: gpt.MicrosoftBasicData,
		Name: partName,
		// GUID       string // partition GUID, can be left blank to auto-generate
		// Attributes uint64 // Attributes flags
	}

	table := gpt.Table {
		Partitions: []*gpt.Partition{
			&part,
		},
		LogicalSectorSize: sectorSize,
		PhysicalSectorSize: 4096,
		// GUID               string       // disk GUID, can be left blank to auto-generate
		ProtectiveMBR: true, // to fool systems that do not support GPT
	}

	if err := theDisk.Partition(&table); err != nil {
		return err
	}

	_, err = theDisk.CreateFilesystem(disk.FilesystemSpec{
		Partition: 1,
		FSType:    filesystem.TypeFat32,
		VolumeLabel: label,
	})
	return err
}
*/
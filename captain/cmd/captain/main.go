package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/rpc"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	diskfs "github.com/diskfs/go-diskfs"
	"github.com/diskfs/go-diskfs/backend/file"
	"github.com/diskfs/go-diskfs/disk"
	"github.com/diskfs/go-diskfs/filesystem"
	"github.com/diskfs/go-diskfs/partition/gpt"
	"golang.org/x/sys/unix"
)

var (
	flagHost   string
	flagDevice string
	flagLabel  string
	flagMount  string
	flagForce  bool
)

func init() {
	flag.StringVar(&flagHost, "host", ":8080", "host:port on which we listen")
	flag.StringVar(&flagDevice, "device", "", "the path to the device that is going to be formatted. ex: /dev/sda")
	flag.StringVar(&flagLabel, "label", "MARLIN_DATA", "the volume label set on the filesystem at the very last stage of the device preparation. Serves as a sentinel for subsequent runs.")
	flag.StringVar(&flagMount, "mnt", "", "the mount point for the drive. empty means no mounting.")
	flag.BoolVar(&flagForce, "force", false, "Skip all checks and force prep the drive.")
}

const (
	maxFATLabelSize = 11
	fsckbin         = "/usr/local/bin/fsck.fat"
)

func checkFlags() {
	if flagHost == "" {
		log.Fatalf("-host cannot be empty")
	}

	if flagDevice == "" {
		log.Fatalf("-device must be provided")
	}

	if flagLabel == "" {
		log.Fatalf("-label cannot be empty")
	}

	if len(flagLabel) > maxFATLabelSize {
		log.Fatalf("-label value too long (> %d chars)", len(flagLabel))
	}
}

var (
	prepMu  sync.RWMutex
	prepped bool
	prepErr error
)

func main() {
	flag.Parse()

	checkFlags()

	if flagForce {
		log.Printf("Force prepping the drive now.")
		if err := prepSSD(flagLabel, flagDevice); err != nil {
			log.Fatalf("while preparing drive: %v", err)
		}
		os.Exit(0)
	}

	go func() {
		var device string
		var err error
		for {
			device, err = findDevice(flagDevice)
			prepMu.Lock()
			prepErr = err
			if err == nil {
				log.Printf("device found")
				prepMu.Unlock()
				break
			}
			log.Printf("%v", err.Error())
			prepMu.Unlock()
			time.Sleep(time.Second)
		}

		for {
			err := checkAndPrep(device, flagLabel)
			prepMu.Lock()
			prepErr = err
			if err == nil {
				log.Printf("SSD at %v ready", flagDevice)
				prepMu.Unlock()
				break
			}
			log.Printf("%v", err.Error())
			prepMu.Unlock()
			time.Sleep(time.Second)
		}

		if flagMount == "" {
			log.Printf("No mounting as -mnt not provided")
			return
		}

		for {
			part, err := partDevice(device)
			if err == nil {
				err = mount(part, flagMount)
			}
			prepMu.Lock()
			prepErr = err
			if err == nil {
				log.Printf("Drive mounted at %v", flagMount)
				prepped = true
				prepMu.Unlock()
				break
			}
			log.Printf("%v", err.Error())
			prepMu.Unlock()
			time.Sleep(time.Second)
		}
		listenForSignals()
	}()

	dp := new(DrivePrepper)
	rpc.Register(dp)
	rpc.HandleHTTP()
	l, err := net.Listen("tcp", flagHost)
	if err != nil {
		log.Fatal("listen error:", err)
	}
	http.Serve(l, nil)
}

func listenForSignals() {
	if flagMount == "" {
		return
	}
	c := make(chan os.Signal, 1)
	signal.Notify(c, unix.SIGTERM)
	go func() {
		for sig := range c {
			log.Printf("received signal %s, cleaning up", sig)

			switch sig {
			case unix.SIGTERM:
				log.Printf("Unmounting %v", flagMount)
				// TODO: use unix.MNT_DETACH?
				if err := syscall.Unmount(flagMount, 0); err != nil {
					log.Printf("unmounting %v failed: %v", flagMount, err)
				}
				os.Exit(0)
			}
		}
	}()
}

type DrivePrepper struct{}

// IsReady checks whether the drive is done being prepared.
// reply == "ready" -> all good
// reply != "" -> critical error
// reply == "" -> no error, but drive not ready yet.
func (p *DrivePrepper) IsReady(noop int, reply *string) error {
	prepMu.RLock()
	isReady := prepped
	err := prepErr
	prepMu.RUnlock()
	if err != nil {
		*reply = err.Error()
		return nil
	}
	if isReady {
		*reply = "ready"
		return nil
	}
	*reply = ""
	return nil
}

func findBlockDevices() ([]string, error) {
	dir, err := os.ReadDir("/sys/block")
	if err != nil {
		return nil, err
	}
	var entries []string
	for _, v := range dir {
		entries = append(entries, filepath.Join("/dev", v.Name()))
	}
	return entries, nil
}

func findDevice(device string) (string, error) {
	devices, err := findBlockDevices()
	if err != nil {
		return "", err
	}

	for _, v := range devices {
		if v == device {
			return v, nil
		}
	}
	return "", fmt.Errorf("%v not found among existing block devices", device)
}

// TODO: do we always assume partition one?
func partDevice(device string) (string, error) {
	// TODO: find it dynamically in existing ones
	if strings.HasPrefix(device, "/dev/sd") {
		return device + "1", nil
	}
	if strings.HasPrefix(device, "/dev/nvme") {
		return device + "p1", nil
	}
	return "", fmt.Errorf("unexpected device: %v", device)
}

func fsck(part string) ([]byte, error) {
	// Looks like -a does the writing, so we indeed don't need -w.
	// AFAIU, -a waits for the whole thing to be done before writing
	// everything, while -w write things ASAP.
	cmd := exec.Command(fsckbin, "-v", "-a", "-V", part)
	//	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	//	return cmd.Run()
	out, err := cmd.Output()
	fmt.Fprintf(os.Stdout, "%s", string(out))
	return out, err
}

func checkFS(part string) (bool, error) {
	var out []byte
	var err error
	if out, err = fsck(part); err != nil {
		// we know we'll get an exit code == 1 if there were things to correct,
		// even if the fixes succeeded. so we do the brute/simple thing, and we
		// simply retry once more. If things were corrected, the second run
		// should return 0. If not, then we do have an actual problem and we
		// let the caller deal with it.
		if out, err = fsck(part); err != nil {
			return false, fmt.Errorf("while trying to fsck %v: %v", part, err)
		}
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	nbFiles := 0
	for sc.Scan() {
		l := sc.Text()
		if !strings.Contains(l, "files,") || !strings.HasSuffix(l, "clusters") {
			continue
		}
		parts := strings.SplitN(l, " ", 5)
		if len(parts) != 5 {
			return false, fmt.Errorf("unexpected number of parts: %d", len(parts))
		}
		nbFiles, err = strconv.Atoi(parts[1])
		if err != nil {
			return false, nil
		}
		break
	}
	// technically should be > 0 , but turns out even when fs seems empty, fsck says 1 file
	if nbFiles > 1 {
		// hasFiles more important than scanning error at this point
		return true, nil
	}
	return false, sc.Err()
}

func checkAndPrep(device, label string) error {
	hasFiles, needsPrep, err := check(device, label)
	if err != nil {
		return fmt.Errorf("while trying to check device: %v", err)
	}

	if !needsPrep {
		log.Printf("SSD was already prepped, nothing to do")
		return nil
	}

	if hasFiles {
		// TODO: make this fatal?
		return fmt.Errorf("Volume label hints that we should partition/format, but device actually already has files, so refusing to proceed out of caution")
	}

	log.Printf("SSD needs partitionning and/or formatting, proceeding.")
	if err := prepSSD(label, device); err != nil {
		return fmt.Errorf("while preparing drive: %v", err)
	}
	return nil
}

// returns error on critical problem. otherwise returns whether the drive needs to be prepped again.
func check(device, label string) (hasFiles, needsPrep bool, err error) {
	theBackend, err := file.OpenFromPath(device, true)
	if err != nil {
		return false, false, err
	}

	theDisk, err := diskfs.OpenBackend(theBackend)
	if err != nil {
		return false, false, err
	}

	// TODO: is it bad to assume we're always going to use the first partition?
	fs, err := theDisk.GetFilesystem(1)
	if err != nil {
		// TODO: could it fail for other reasons?
		log.Printf("no existing filesystem found on device: %v", err)
		return false, true, nil
	}

	part, err := partDevice(device)
	if err != nil {
		return false, false, err
	}
	hasFiles, err = checkFS(part)
	if err != nil {
		return hasFiles, false, err
	}

	lbl := fs.Label()
	if lbl == "" {
		log.Printf("existing filesystem was found, but with no label")
		return hasFiles, true, nil
	}
	// Should never happen, but can't hurt.
	if len(lbl) > maxFATLabelSize {
		return hasFiles, false, fmt.Errorf("label on filesystem (%v) longer than max allowed size (%d)", lbl, maxFATLabelSize)
	}
	if lbl != label {
		log.Printf("existing filesystem was found, but with wrong label (%v)", lbl)
		return hasFiles, true, nil
	}

	return hasFiles, false, nil
}

const (
	partName           = "marlin"
	sectorSize         = 512
	physicalSectorSize = 4096
	// could be as low as 34 really, because GPT headers are 33 sectors,
	// but using 40 for alignment reasons (factor of 8)
	startSector = 40
)

func prepSSD(label, device string) error {
	theBackend, err := file.OpenFromPath(device, false)
	if err != nil {
		return err
	}

	theDisk, err := diskfs.OpenBackend(theBackend)
	if err != nil {
		return err
	}

	sizeInSectors := uint64(theDisk.Size) / uint64(sectorSize)
	sizeInSectors -= uint64(startSector)
	sizeInSectors -= 33 // leave space for backup GPT at the end

	part := gpt.Partition{
		Start: uint64(startSector),
		Size:  sizeInSectors * uint64(sectorSize),
		Type:  gpt.MicrosoftBasicData,
		// Type: gpt.EFISystemPartition,
		Name: partName,
	}

	table := gpt.Table{
		Partitions: []*gpt.Partition{
			&part,
		},
		LogicalSectorSize:  sectorSize,
		PhysicalSectorSize: physicalSectorSize,
		ProtectiveMBR:      true, // to comply with systems that do not support GPT
	}

	if err := theDisk.Partition(&table); err != nil {
		return err
	}

	log.Printf("Creating filesystem with label %s", label)
	theFS, err := theDisk.CreateFilesystem(disk.FilesystemSpec{
		Partition:   1,
		FSType:      filesystem.TypeFat32,
		VolumeLabel: label,
	})
	if err != nil {
		return err
	}
	err = theFS.SetLabel(label)
	return err
}

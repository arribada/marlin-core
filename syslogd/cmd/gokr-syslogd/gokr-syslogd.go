// Binary gokr-syslogd is a remote syslog server that writes all received
// messages into files on local disk. Files that are no longer in use (no new
// messages will be written to them) will be compressed and deleted after 7
// days.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"cloud.google.com/go/logging"
	"github.com/google/renameio/v2"
	"github.com/klauspost/compress/zstd"
	"gopkg.in/mcuadros/go-syslog.v2"
)

const basenameFormat = "2006-01-02.log"

var (
	flagOutdir          string
	flagListenAddrs     string
	flagTestGoogLogging bool
	flagGoogCredentials string
)

var (
	// logRateLimited throttles printing error message. This is particularly
	// important when the gokr-syslogd output itself is sent to gokr-syslogd, which
	// could cause infinite log message loops without rate limiting.
	//
	// When the value is 0, a log message can be printed. A background goroutine
	// resets the value to 0 once a second.
	logRateLimited uint32

	glog *logging.Logger
)

func init() {
	go func() {
		for range time.Tick(1 * time.Second) {
			atomic.StoreUint32(&logRateLimited, 0)
		}
	}()
	flag.StringVar(&flagOutdir, "outdir", "/perm/syslogd", "directory to which to write syslog to")
	flag.StringVar(&flagListenAddrs, "listen", "127.0.0.1:5514", "[host]:port listen address")
	flag.BoolVar(&flagTestGoogLogging, "testgoog", false, "test logging with goog cloud logging on startup and exit immediately")
	flag.StringVar(&flagGoogCredentials, "goog_creds", "", "google application credentials file. leave empty to disable goog logging.")
}

type fileKey struct {
	hostname string
	basename string
}

type openFile struct {
	f       *os.File
	lastUse time.Time
}

type server struct {
	dir   string
	files map[fileKey]*openFile
}

func (s *server) openFile(key fileKey) (*os.File, error) {
	fn := filepath.Join(s.dir, key.hostname, key.basename)
	if err := os.MkdirAll(filepath.Dir(fn), 0755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(fn, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, err
	}
	// os.O_APPEND results in the kernel seeking to the end of the file on
	// *every write*, which is unnecessary for our use-case. Instead, we seek to
	// the end once when opening a file, which is a no-op for newly created
	// files, and positions us correctly for an already-existing file.
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return nil, err
	}
	return f, nil
}

func (s *server) toDeleteLogFileNames(now time.Time) ([]string, error) {
	oldestToKeep := now.Add(-7 * 24 * time.Hour).Format(basenameFormat)

	var toDeleteLogFileNames []string

	hostDirs, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	for _, hostDir := range hostDirs {
		dir := filepath.Join(s.dir, hostDir.Name())
		logFiles, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		logFileNames := make([]string, 0, len(logFiles))
		for _, logFile := range logFiles {
			if !strings.HasSuffix(logFile.Name(), ".log.zst") {
				continue // skip already compressed file
			}
			logFileNames = append(logFileNames, filepath.Join(dir, logFile.Name()))
		}
		// Exclude all log files that might still be in use
		toDelete := make([]string, 0, len(logFileNames))
		for _, fn := range logFileNames {
			if strings.Compare(filepath.Join(dir, oldestToKeep), fn) > 0 {
				toDelete = append(toDelete, fn)
			}
		}
		toDeleteLogFileNames = append(toDeleteLogFileNames, toDelete...)
	}
	return toDeleteLogFileNames, nil
}

func (s *server) coldLogFileNames(now time.Time) ([]string, error) {
	// We accept log messages for up to 24 hours earlier
	earliestInUse := now.Add(-24 * time.Hour).Format(basenameFormat)
	currentlyInUse := now.Format(basenameFormat)

	var coldLogFileNames []string

	hostDirs, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	for _, hostDir := range hostDirs {
		dir := filepath.Join(s.dir, hostDir.Name())
		logFiles, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		logFileNames := make([]string, 0, len(logFiles))
		for _, logFile := range logFiles {
			if !strings.HasSuffix(logFile.Name(), ".log") {
				continue // skip already compressed file
			}
			logFileNames = append(logFileNames, filepath.Join(dir, logFile.Name()))
		}
		// Exclude all log files that might still be in use
		cold := logFileNames
		for _, earliest := range []string{
			filepath.Join(dir, earliestInUse),
			filepath.Join(dir, currentlyInUse),
		} {
			i, found := sort.Find(len(cold), func(i int) int {
				return strings.Compare(earliest, cold[i])
			})
			if found {
				cold = cold[:i]
			}
		}
		coldLogFileNames = append(coldLogFileNames, cold...)
	}
	return coldLogFileNames, nil
}

func compressFile(fn string) error {
	src, err := os.Open(fn)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := renameio.TempFile("", fn+".zst")
	if err != nil {
		return err
	}
	defer dst.Cleanup()
	wr, err := zstd.NewWriter(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(wr, src); err != nil {
		return err
	}
	if err := wr.Close(); err != nil {
		return err
	}
	if err := dst.CloseAtomicallyReplace(); err != nil {
		return err
	}
	return os.Remove(fn)
}

func (s *server) compressOldLogs() error {
	cold, err := s.coldLogFileNames(time.Now())
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no log files written yet
		}
		return err
	}
	for _, fn := range cold {
		log.Printf("compressing %s to %s.zst", fn, fn)
		if err := compressFile(fn); err != nil {
			log.Printf("compressing %s: %v", fn, err)
		}
	}
	return nil
}

func (s *server) deleteOldLogs() error {
	toDelete, err := s.toDeleteLogFileNames(time.Now())
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no log files written yet
		}
		return err
	}
	for _, fn := range toDelete {
		log.Printf("deleting log file older than 7 days: %s", fn)
		if err := os.Remove(fn); err != nil {
			log.Printf("deleting %s: %v", fn, err)
		}
	}
	return nil
}

func gokrsyslogd() error {
	srv := server{
		dir:   flagOutdir,
		files: make(map[fileKey]*openFile),
	}

	// Start periodic log compression/deletion in the background, not blocking
	// server startup.
	go func() {
		for ; ; time.Sleep(1 * time.Hour) {
			if err := srv.compressOldLogs(); err != nil {
				log.Printf("compressing old logs: %v", err)
			}
			if err := srv.deleteOldLogs(); err != nil {
				log.Printf("deleting old logs: %v", err)
			}
		}
	}()

	// TODO: how does flow control work? this is a blocking channel, where does
	// backpressure go?
	channel := make(syslog.LogPartsChannel)
	syslogsrv := syslog.NewServer()
	// RFC3164 seems to be what Go’s standard library log/syslog package uses.
	// The other two available formats (RFC6587, RFC5424) result in garbage.
	syslogsrv.SetFormat(syslog.RFC3164)
	addrs := strings.Split(flagListenAddrs, ",")
	for _, addr := range addrs {
		if err := syslogsrv.ListenUDP(addr); err != nil {
			return err
		}
	}
	syslogsrv.SetHandler(syslog.NewChannelHandler(channel))
	if err := syslogsrv.Boot(); err != nil {
		return err
	}
	log.Printf("writing to %s all remote syslog received on %s", flagOutdir, flagListenAddrs)

	// Every 100 syslog messages, look through currently open files to close
	// unused ones.
	const closeFrequency = 100
	stride := closeFrequency
	go func(channel syslog.LogPartsChannel) {
		for logParts := range channel {
			// This is an example logParts value: map[
			//   client:10.0.0.16:58045
			//   content:Try `iptables -h' or 'iptables --help' for more information.
			//   facility:0
			//   hostname:gokrazy
			//   priority:6 // gokrazy sends all messages at LOG_INFO
			//   severity:6
			//   tag:iptables // gokrazy sends the basename of the binary
			//   timestamp:2022-08-13 14:41:30 +0200 +0200
			// tls_peer:]
			var (
				hostname  string
				timestamp time.Time
				tag       string
				content   string
			)
			if v, ok := logParts["hostname"]; ok {
				hostname = v.(string)
			}
			if v, ok := logParts["content"]; ok {
				content = v.(string)
			}
			if v, ok := logParts["timestamp"]; ok {
				timestamp = v.(time.Time)
			}
			if v, ok := logParts["tag"]; ok {
				tag = v.(string)
			}
			if hostname == "" ||
				tag == "" ||
				content == "" ||
				timestamp.IsZero() {
				continue
			}

			// Reject too old timestamps to avoid tampering and to make it safe
			// to compress/rotate old files.
			if time.Since(timestamp) > 24*time.Hour {
				if atomic.SwapUint32(&logRateLimited, 1) == 0 {
					log.Printf("dropping message with timestamp with too large clock drift: timestamp %v", timestamp)
				}
				continue
			}

			basename := timestamp.Format(basenameFormat)
			key := fileKey{
				hostname: hostname,
				basename: basename,
			}
			of, ok := srv.files[key]
			if !ok {
				f, err := srv.openFile(key)
				if err != nil {
					if atomic.SwapUint32(&logRateLimited, 1) == 0 {
						log.Printf("error opening log file: %v", err)
					}
					continue
				}
				of = &openFile{
					f: f,
				}
				srv.files[key] = of
			}
			of.lastUse = time.Now()
			fmt.Fprintf(of.f, "%s: %s\n", tag, content)
			if glog != nil {
				// TODO: we could easily filter on tag already here if we wanted to. And provide the tags as a flag to the program.
				// But let's see first how easy it would be to filter by tag too on goog logging.
				glog.Log(logging.Entry{Payload: MyEntry{
					Hostname: hostname,
					Tag:      tag,
					Content:  content,
				}})
			}

			stride--
			if stride <= 0 {
				stride = closeFrequency
				for key, of := range srv.files {
					if time.Since(of.lastUse) < 10*time.Minute {
						continue
					}
					log.Printf("closing unused log file for key=%v", key)
					// close old log file
					if err := of.f.Close(); err != nil {
						if atomic.SwapUint32(&logRateLimited, 1) == 0 {
							log.Printf("error opening log file: %v", err)
						}
					}
					delete(srv.files, key)
				}
			}
		}
	}(channel)

	syslogsrv.Wait()
	log.Printf("srv.Wait() returned, last error: %v", syslogsrv.GetLastError())

	return nil
}

type MyEntry struct {
	Hostname string
	Tag      string
	Content  string
}

func initGoogLogging() *logging.Client {
	if flagGoogCredentials == "" {
		log.Println("no google application credentials file provided")
		return nil
	}
	if err := os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", flagGoogCredentials); err != nil {
		log.Println(err)
		return nil
	}
	ctx := context.Background()
	client, err := logging.NewClient(ctx, "nimble-crane-448814-k5")
	if err != nil {
		log.Println(err)
		return nil
	}
	// TODO: add hostname?
	glog = client.Logger("marlin")
	return client
}

func main() {
	flag.Parse()

	client := initGoogLogging()
	if client != nil {
		defer func() {
			if err := client.Close(); err != nil {
				log.Print(err)
			}
		}()
	}

	if flagTestGoogLogging {
		if glog == nil {
			log.Fatal("goog logger not initialized, cannot test logging")
		}
		glog.Log(logging.Entry{Payload: MyEntry{
			Hostname: "fake hostname",
			Tag:      "diagnostic",
			Content:  "Test from syslogd",
		}})
		return
	}

	if err := gokrsyslogd(); err != nil {
		log.Fatal(err)
	}
}

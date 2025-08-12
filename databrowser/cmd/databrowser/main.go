// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/arribada/marlin-core/common/pkg/utils"
	"github.com/mpl/basicauth"
)

var (
	flagHost      string
	flagVerbose   bool
	flagUserPass  string
	flagUrlPrefix string
	flagOutDir    string
	flagCaptain   string
)

func init() {
	// TODO: make it listen only on TS interface?
	flag.StringVar(&flagHost, "host", ":7144", "listening port and hostname")
	flag.StringVar(&flagUserPass, "userpass", "", "basic auth username and password. Also read from AUTH_USERPASS env var.")
	flag.StringVar(&flagUrlPrefix, "urlprefix", "", "url path prefix")
	flag.BoolVar(&flagVerbose, "v", false, "be verbose")
	flag.StringVar(&flagOutDir, "outdir", "./out", "output data directory")
	flag.StringVar(&flagCaptain, "captain", "localhost:7143", "wait for captain (to have mounted an external drive at this host:port. Skip if empty.")
}

var (
	up *basicauth.UserPass
)

func initStuff() {
	if flagCaptain == "" {
		if err := os.MkdirAll(flagOutDir, 0750); err != nil {
			log.Fatal(err)
		}
	}

	if flagUserPass == "" {
		flagUserPass = os.Getenv("AUTH_USERPASS")
	}
	// we check it here, because NewUserPass silently allows for an empty one.
	// Better safe than sorry.
	if flagUserPass == "" {
		log.Fatalf("userpass not provided")
	}

	if err := utils.WaitForDrive(flagCaptain); err != nil {
		log.Fatal(err)
	}

	var err error
	up, err = basicauth.NewUserPass(flagUserPass)
	if err != nil {
		log.Fatal(err)
	}
}

func main() {
	flag.Parse()

	initStuff()

	Server()
}

type sortedFiles []os.FileInfo

func (s sortedFiles) Len() int { return len(s) }

func (s sortedFiles) Less(i, j int) bool {
	return strings.ToLower(s[i].Name()) < strings.ToLower(s[j].Name())
}

func (s sortedFiles) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

func sortedDirList(w http.ResponseWriter, f http.File, needsPrefix bool) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, "<pre>\n")
	var sdirs sortedFiles
	for {
		dirs, err := f.Readdir(100)
		if err != nil || len(dirs) == 0 {
			break
		}
		sdirs = append(sdirs, dirs...)
	}
	sort.Sort(sdirs)
	for _, d := range sdirs {
		name := d.Name()
		if d.IsDir() {
			name += "/"
		}
		href := name
		// Since all hrefs are relative to the current dir, we only prepend
		// flagUrlPrefix if we are at the root dir.
		if needsPrefix {
			// not using path.Join because it removes trailing "/" , which is needed to signal
			// that we are in a dir
			href = flagUrlPrefix + "/" + href
		}
		// TODO htmlescape
		fmt.Fprintf(w, "<a href=\"%s\">%s</a>\n", href, name)
	}
	fmt.Fprintf(w, "</pre>\n")
}

// modtime is the modification time of the resource to be served, or IsZero().
// return value is whether this request is now complete.
func checkLastModified(w http.ResponseWriter, r *http.Request, modtime time.Time) bool {
	if modtime.IsZero() {
		return false
	}

	// The Date-Modified header truncates sub-second precision, so
	// use mtime < t+1s instead of mtime <= t to check for unmodified.
	if t, err := time.Parse(http.TimeFormat, r.Header.Get("If-Modified-Since")); err == nil && modtime.Before(t.Add(1*time.Second)) {
		w.WriteHeader(http.StatusNotModified)
		return true
	}
	w.Header().Set("Last-Modified", modtime.UTC().Format(http.TimeFormat))
	return false
}

// copied from stdlib, and modified to server sorted listing
// name is '/'-separated, not filepath.Separator.
func serveFile(w http.ResponseWriter, r *http.Request, fs http.FileSystem, name string, needsPrefix bool) {
	const indexPage = "/index.html"

	f, err := fs.Open(name)
	if err != nil {
		// TODO expose actual error?
		http.NotFound(w, r)
		return
	}
	defer f.Close()

	d, err1 := f.Stat()
	if err1 != nil {
		// TODO expose actual error?
		http.NotFound(w, r)
		return
	}

	// use contents of index.html for directory, if present
	if d.IsDir() {
		index := name + indexPage
		ff, err := fs.Open(index)
		if err == nil {
			defer ff.Close()
			dd, err := ff.Stat()
			if err == nil {
				name = index
				d = dd
				f = ff
			}
		}
	}

	if flagVerbose {
		log.Printf("Serving %v", d.Name())
	}
	// Still a directory? (we didn't find an index.html file)
	if d.IsDir() {
		if checkLastModified(w, r, d.ModTime()) {
			return
		}
		sortedDirList(w, f, needsPrefix)
		return
	}

	// serverContent will check modification time
	http.ServeContent(w, r, d.Name(), d.ModTime(), f)
}

type fileServer struct{}

func (fs fileServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	urlPath := r.URL.Path
	needsPrefix := false
	if flagUrlPrefix != "" {
		urlPath = strings.TrimPrefix(urlPath, flagUrlPrefix)
		if urlPath == "" || urlPath == "/" {
			needsPrefix = true
		}
	}
	dir, file := filepath.Split(filepath.Join(flagOutDir, urlPath))
	serveFile(w, r, http.Dir(dir), file, needsPrefix)
}

type reportHandler struct {
	day string
}

func (rh reportHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var theDay string
	if _, err := time.Parse(time.DateOnly, rh.day); err != nil {
		lastDay, err := getPreviousFishingDay(flagOutDir)
		if err != nil {
			http.Error(w, fmt.Sprintf("while trying to find last fishing day: %v", err), 500)
			return
		}
		theDay = lastDay
	} else {
		theDay = rh.day
	}

	dt := []string{"gps", "video"}
	out, err := doDay(flagOutDir, theDay, dt)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Write([]byte(out))
}

type allHandler struct {
	fs fileServer
}

func (ah allHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rep := r.FormValue("report")
	if rep != "" {
		reportHandler{day: rep}.ServeHTTP(w, r)
		return
	}

	// TODO: any other interesting queries?

	ah.fs.ServeHTTP(w, r)
}

func Server() {
	sh := basicauth.UserPassHandler{
		UP: up,
		H: allHandler{
			fs: fileServer{},
		},
	}

	if flagUrlPrefix != "" {
		flagUrlPrefix = strings.TrimSuffix(flagUrlPrefix, "/")
		http.Handle(flagUrlPrefix+"/", &sh)
	} else {
		http.Handle("/", &sh)
	}

	log.Fatal(http.ListenAndServe(flagHost, nil))
}

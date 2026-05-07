// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/arribada/marlin-common/pkg/utils"
	"github.com/mpl/basicauth"
)

var (
	flagHost     string
	flagVerbose  bool
	flagUserPass string
	flagCodeDir  string
	flagDataDir  string
	flagCaptain  string
)

func init() {
	// TODO: make it listen only on TS interface?
	flag.StringVar(&flagHost, "host", ":7144", "listening port and hostname")
	flag.StringVar(&flagUserPass, "userpass", "", "basic auth username and password. Also read from AUTH_USERPASS env var.")
	flag.BoolVar(&flagVerbose, "v", false, "be verbose")
	flag.StringVar(&flagDataDir, "data", "/perm/marlin", "the directory where the served data lives. Served at /rawdata (as html) and /data (as json).")
	flag.StringVar(&flagCodeDir, "code", "./dashboard", "the .html, .js, etc. resources directory, served at /")
	flag.StringVar(&flagCaptain, "captain", "localhost:7143", "wait for captain (to have mounted an external drive at this host:port. Skip if empty.")
}

var (
	up *basicauth.UserPass
)

const (
	dataPrefix    = "/data/"
	rawdataPrefix = "/rawdata/"
)

func initStuff() {
	if flagCaptain == "" {
		if err := os.MkdirAll(flagDataDir, 0750); err != nil {
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

	if flagCaptain != "" {
		if err := utils.WaitForDrive(flagCaptain); err != nil {
			log.Fatal(err)
		}
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

func sortedDirList(w http.ResponseWriter, f http.File) {
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
func serveFile(w http.ResponseWriter, r *http.Request, fs http.FileSystem, name string) {
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
		sortedDirList(w, f)
		return
	}

	// serverContent will check modification time
	http.ServeContent(w, r, d.Name(), d.ModTime(), f)
}

type fileServer struct{}

func (fs fileServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	fullPath := filepath.Join(flagDataDir, strings.TrimPrefix(r.URL.Path, rawdataPrefix))
	dir, file := filepath.Split(fullPath)
	serveFile(w, r, http.Dir(dir), file)
}

type reportHandler struct {
	day string
}

func (rh reportHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var theDay string
	if _, err := time.Parse(time.DateOnly, rh.day); err != nil {
		lastDay, err := getPreviousFishingDay(flagDataDir)
		if err != nil {
			http.Error(w, fmt.Sprintf("while trying to find last fishing day: %v", err), 500)
			return
		}
		theDay = lastDay
	} else {
		theDay = rh.day
	}

	dt := []string{"gps", "video"}
	out, err := doDay(flagDataDir, theDay, dt)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Write([]byte(out))
}

type dataServer struct{}

func (s dataServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	fullPath := filepath.Join(flagDataDir, strings.TrimPrefix(r.URL.Path, dataPrefix))

	fi, err := os.Stat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			http.NotFound(w, r)
			return
		}
		log.Printf("stat error for %v: %v", fullPath, err)
		http.Error(w, "", 500)
		return
	}

	if !fi.IsDir() {
		f, err := os.Open(fullPath)
		if err != nil {
			if os.IsNotExist(err) {
				http.NotFound(w, r)
				return
			}
			log.Printf("open error for %v: %v", fullPath, err)
			http.Error(w, "", 500)
			return
		}
		defer f.Close()
		if flagVerbose {
			log.Printf("Serving %v", fi.Name())
		}
		http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
		return
	}

	/*
		// TODO: do we want that?
		if checkLastModified(w, r, fi.ModTime()) {
			return
		}
	*/

	serveDir(w, r, fullPath)
}

func serveDir(w http.ResponseWriter, r *http.Request, fullPath string) {
	f, err := os.Open(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			http.NotFound(w, r)
			return
		}
		log.Printf("open error for %v: %v", fullPath, err)
		http.Error(w, "", 500)
		return
	}
	defer f.Close()
	ent, err := f.Readdirnames(-1)
	if err != nil {
		if os.IsNotExist(err) {
			http.NotFound(w, r)
			return
		}
		log.Printf("readdir error for %v: %v", fullPath, err)
		http.Error(w, "", 500)
		return
	}

	data, err := json.Marshal(&ent)
	if err != nil {
		log.Printf("json marshal error for %v: %v", fullPath, err)
		http.Error(w, "", 500)
		return
	}
	w.Write(data)
}

type srcServer struct{}

func (s srcServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, filepath.Join(flagCodeDir, r.URL.Path))
}

type topHandler struct {
	sub http.Handler
}

func (top topHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rep := r.FormValue("report")
	if rep != "" {
		reportHandler{day: rep}.ServeHTTP(w, r)
		return
	}

	// TODO: any other interesting queries?

	top.sub.ServeHTTP(w, r)
}

func Server() {
	srcHandler := basicauth.UserPassHandler{
		UP: up,
		H: topHandler{
			sub: srcServer{},
		},
	}

	dataHandler := basicauth.UserPassHandler{
		UP: up,
		H:  dataServer{},
	}

	rawHandler := basicauth.UserPassHandler{
		UP: up,
		H:  fileServer{},
	}

	http.Handle(dataPrefix, &dataHandler)
	http.Handle(rawdataPrefix, &rawHandler)
	http.Handle("/", &srcHandler)

	log.Fatal(http.ListenAndServe(flagHost, nil))
}

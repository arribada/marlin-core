// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package watcher

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azfile/directory"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azfile/fileerror"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azfile/share"
	"github.com/arribada/marlin-core/common/pkg/datatype"
)

func TestArchiveFile(t *testing.T) {
	dir := t.TempDir()
	dataType := datatype.GPS
	tm := time.Now()

	name, err := datatype.NewFileName(dataType, tm)
	if err != nil {
		t.Fatal(err)
	}
	fp := filepath.Join(dir, name)
	f, err := os.Create(fp)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	ew := &EventsWatcher{
		up: &uploader{},
	}
	newName, err := ew.archiveFile(dataType, fp)
	if err != nil {
		t.Fatal(err)
	}

	parts := strings.Split(newName, string(filepath.Separator))
	filename := parts[len(parts)-1]
	dt := parts[len(parts)-2]
	day := parts[len(parts)-3]

	if filename != name {
		t.Fatalf("unexpected filename: wanted %s, got %s", name, filename)
	}

	dtwant, _ := datatype.DirType(dataType)
	if dt != dtwant {
		t.Fatalf("unexpected data type dir: wanted %s, got %s", dtwant, dt)
	}

	today := tm.Format(time.DateOnly)
	if day != today {
		t.Fatalf("unexpected day dir: wanted %s, got %s", today, day)
	}
}

func TestUploadAll(t *testing.T) {
	connString := os.Getenv("AZURE_CONN")
	if connString == "" {
		t.Skip("Skipping because AZURE_CONN not set")
	}
	shareName := "uploadtests"
	dataDir := filepath.Join("testdata", shareName)

	if err := deleteShareContents(connString, shareName); err != nil {
		t.Fatal(err)
	}

	// we do not mark for upload the ones in 2025-09-22 on purpose
	if err := markAllForUpload(filepath.Join(dataDir, "2025-09-23/gps")); err != nil {
		t.Fatal(err)
	}

	cfg := Config{
		Enabled:        true,
		DataDir:        dataDir,
		Verbose:        true,
		AzureConn:      connString,
		AzureShare:     shareName,
		UploadInterval: 300,
	}

	up, err := newUploader(cfg)
	if err != nil {
		t.Fatal(err)
	}

	if err := up.uploadAll(); err != nil {
		t.Fatal(err)
	}

	// verify the files in 2025-09-23/gps are there
	wantFiles := map[string]bool{
		"gps-2025-09-23T11_38_25Z.json": false,
		"gps-2025-09-23T11_39_26Z.json": false,
		"gps-2025-09-23T11_40_27Z.json": false,
	}
	cl, err := share.NewClientFromConnectionString(connString, shareName, nil)
	if err != nil {
		t.Fatal(err)
	}
	rootCl := cl.NewRootDirectoryClient()
	dayCl := rootCl.NewSubdirectoryClient("2025-09-23")
	dtCl := dayCl.NewSubdirectoryClient("gps")
	pager := dtCl.NewListFilesAndDirectoriesPager(nil)
	for pager.More() {
		resp, err := pager.NextPage(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, fil := range resp.Segment.Files {
			name := *(fil.Name)
			if _, ok := wantFiles[name]; !ok {
				t.Fatalf("uploaded file %v not in the list of expected files", name)
			}
			wantFiles[name] = true
		}
	}
	for k, v := range wantFiles {
		if !v {
			t.Fatalf("%v was not found among uploaded files", k)
		}
	}

	// verify there's nothing in 2025-09-22/gps
	dayCl = rootCl.NewSubdirectoryClient("2025-09-22")
	dtCl = dayCl.NewSubdirectoryClient("gps")
	pager = dtCl.NewListFilesAndDirectoriesPager(nil)
	for pager.More() {
		resp, err := pager.NextPage(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, fil := range resp.Segment.Files {
			name := *(fil.Name)
			t.Fatalf("found file in %v in 2025-09-22/gps", name)
		}
	}
}

func deleteShareContents(connString, shareName string) error {
	cl, err := share.NewClientFromConnectionString(connString, shareName, nil)
	if err != nil {
		return err
	}
	_, err = cl.Create(context.TODO(), nil)
	if err == nil {
		// did not exist yet, no need to cleanup
		return nil
	}
	if !fileerror.HasCode(err, fileerror.ShareAlreadyExists) {
		return fmt.Errorf("Azure share creation failed: %v", err)
	}

	// already exists, need to cleanup

	rootCl := cl.NewRootDirectoryClient()
	return recurseDelete(rootCl)
}

func recurseDelete(dirCl *directory.Client) error {
	pager := dirCl.NewListFilesAndDirectoriesPager(nil)
	for pager.More() {
		resp, err := pager.NextPage(context.Background())
		if err != nil {
			return err
		}
		for _, dir := range resp.Segment.Directories {
			cl := dirCl.NewSubdirectoryClient(*(dir.Name))
			if err := recurseDelete(cl); err != nil {
				return err
			}
			if _, err := cl.Delete(context.TODO(), nil); err != nil {
				return err
			}
		}
		for _, fil := range resp.Segment.Files {
			cl := dirCl.NewFileClient(*(fil.Name))
			if _, err := cl.Delete(context.TODO(), nil); err != nil {
				return err
			}
		}
	}
	return nil
}

func markAllForUpload(dir string) error {
	ent, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	for _, v := range ent {
		name := v.Name()
		if isUploadMarker(name) {
			continue
		}
		fullPath := filepath.Join(dir, v.Name())
		if err := markForUpload(fullPath); err != nil {
			return err
		}
	}

	return nil
}

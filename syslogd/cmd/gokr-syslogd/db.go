// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"cloud.google.com/go/logging"
	_ "modernc.org/sqlite"
)

// TODO: close DB
var (
	// TODO: review how transactions are implemented in sqlite, and reconsider that mutex
	dbMu          sync.RWMutex
	db            *sql.DB
	logAgentPause = time.Minute
)

func initDB() error {
	if glog == nil {
		return nil
	}
	sqlFile := filepath.Join(flagOutdir, "glog.db")
	var err error
	db, err = sql.Open("sqlite", sqlFile)
	if err != nil {
		return err
	}

	if _, err := db.Exec("CREATE TABLE IF NOT EXISTS log (id INTEGER PRIMARY KEY AUTOINCREMENT, hostname TEXT, tag TEXT, content TEXT);"); err != nil {
		return err
	}

	go logAgent()

	return nil
}

func logToDB(hostname, tag, content string) error {
	// No need to persist to DB if we're not going to push to goog log afterwards
	if glog == nil {
		return nil
	}
	// TODO: full list from somewhere else later.
	if tag != "captain" &&
		tag != "cellular" &&
		tag != "collect" &&
		tag != "selfupdate" {
		return nil
	}

	dbMu.Lock()
	defer dbMu.Unlock()
	if _, err := db.Exec("INSERT INTO log (hostname, tag, content) VALUES(?, ?, ?);",
		hostname, tag, content); err != nil {
		return err
	}

	return nil
}

type MyEntryID struct {
	id  int64
	ent MyEntry
}

func logsFromDB() ([]MyEntryID, error) {
	if glog == nil {
		return nil, nil
	}

	dbMu.RLock()
	// TODO: can probably be tighter, just after the call to Query
	defer dbMu.RUnlock()
	// TODO: looks like LIMIT does not work?
	rows, err := db.Query("SELECT id, hostname, tag, content FROM log ORDER BY id ASC LIMIT 500;")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ents []MyEntryID
	for rows.Next() {
		var (
			id       int64
			hostname string
			tag      string
			content  string
		)
		if err := rows.Scan(&id, &hostname, &tag, &content); err != nil {
			return nil, err
		}
		ent := MyEntry{Hostname: hostname, Tag: tag, Content: content}
		ents = append(ents, MyEntryID{id: id, ent: ent})
	}
	if flagVerbose && len(ents) > 0 {
		log.Printf("Fetched entries from DB: from %d to %d", ents[0].id, ents[len(ents)-1].id)
	}

	return ents, nil
}

func rmFromDB(done []int64) error {
	if glog == nil {
		return nil
	}

	dbMu.Lock()
	defer dbMu.Unlock()
	const batchSize = 500
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	start := 0
	for {
		if start >= len(done) {
			break
		}
		end := start + batchSize
		if end > len(done) {
			end = len(done)
		}
		batch := done[start:end]

		// TODO: can probably use a string builder instead
		placeholders := make([]string, len(batch))
		args := make([]interface{}, len(batch))
		for i, id := range batch {
			placeholders[i] = "?"
			args[i] = id
		}
		if flagVerbose && len(batch) > 0 {
			log.Printf("Removing processed entries from DB: from %d to %d", batch[0], batch[len(batch)-1])
		}

		query := fmt.Sprintf("DELETE FROM log WHERE id IN (%s)", strings.Join(placeholders, ","))

		_, err := tx.Exec(query, args...)
		if err != nil {
			return fmt.Errorf("failed to delete batch: %w", err)
		}
		start = end
	}

	return tx.Commit()
}

func logAgent() {
	for {
		// TODO: maybe switch (back) to async logging later. more efficient,
		// but more complicated for keeping track of (connection) failures.

		ents, err := logsFromDB()
		if err != nil {
			log.Fatalf("while getting logs from DB: %v", err)
		}

		var done []int64
		// TODO: will I get rate-limited if there's a lot of them?
		for _, v := range ents {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if err := glog.LogSync(ctx, logging.Entry{Payload: v.ent}); err != nil {
				// TODO: suppress that logging when it fails by context?
				log.Printf("failed to log entry from DB to goog logging: %v", err)
				cancel()
				break
			}
			done = append(done, v.id)
			cancel()
		}
		if flagVerbose && len(done) > 0 {
			log.Printf("Sync to goog logging entries from DB: from %d to %d", done[0], done[len(done)-1])
		}

		if err := rmFromDB(done); err != nil {
			log.Fatalf("while removing done entries from DB: %v", err)
		}

		time.Sleep(logAgentPause)
	}
}

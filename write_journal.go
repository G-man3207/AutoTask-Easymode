package main

import (
	"autotask-easymode/internal/atomicfile"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"time"
)

type writeJournal struct {
	Operations map[string]*writeJournalRecord `json:"operations"`
	path       string
}

type writeJournalRecord struct {
	Key      string    `json:"key"`
	Action   string    `json:"action"`
	TicketID int64     `json:"ticketId,omitempty"`
	EntryIDs []int64   `json:"entryIds,omitempty"`
	Closed   bool      `json:"closed,omitempty"`
	Created  time.Time `json:"createdAt"`
	Updated  time.Time `json:"updatedAt"`
}

func loadWriteJournal(path string) (*writeJournal, error) {
	j := &writeJournal{path: path, Operations: map[string]*writeJournalRecord{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return j, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, j); err != nil {
		return nil, err
	}
	j.path = path
	if j.Operations == nil {
		j.Operations = map[string]*writeJournalRecord{}
	}
	return j, nil
}

func (j *writeJournal) save() error {
	return atomicfile.WriteJSON(j.path, j, 0o600)
}

func (j *writeJournal) begin(action, key string, now time.Time) (*writeJournalRecord, error) {
	if rec, ok := j.Operations[key]; ok {
		return rec, nil
	}
	rec := &writeJournalRecord{Key: key, Action: action, Created: now, Updated: now}
	j.Operations[key] = rec
	return rec, j.save()
}

func (j *writeJournal) touch(rec *writeJournalRecord, now time.Time) error {
	rec.Updated = now
	return j.save()
}

func (j *writeJournal) complete(key string) error {
	delete(j.Operations, key)
	return j.save()
}

func operationKey(action string, payload any) (string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte(action+":"), data...))
	return hex.EncodeToString(sum[:]), nil
}

func (a *App) beginWriteOperation(action string, payload any) (*writeJournal, *writeJournalRecord, error) {
	key, err := operationKey(action, payload)
	if err != nil {
		return nil, nil, err
	}
	journal, err := loadWriteJournal(a.journal)
	if err != nil {
		return nil, nil, err
	}
	rec, err := journal.begin(action, key, a.now())
	if err != nil {
		return nil, nil, err
	}
	return journal, rec, nil
}

func ensureEntrySlots(ids []int64, count int) []int64 {
	if len(ids) >= count {
		return ids
	}
	return append(ids, make([]int64, count-len(ids))...)
}

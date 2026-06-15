// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package mongodbreceiver // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/mongodbreceiver"

import (
	"encoding/json"
	"sort"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// profileDoc represents a single document from system.profile.
type profileDoc struct {
	TS             time.Time `bson:"ts"`
	NS             string    `bson:"ns"`
	Op             string    `bson:"op"`
	Millis         int64     `bson:"millis"`
	CPUNanos       int64     `bson:"cpuNanos"`
	ResponseLength int64     `bson:"responseLength"`
	KeysExamined   int64     `bson:"keysExamined"`
	DocsExamined   int64     `bson:"docsExamined"`
	NReturned      int64     `bson:"nreturned"`
	PlanSummary    string    `bson:"planSummary"`
	Command        bson.D    `bson:"command"`
}

// slowQueryEntry is the single shared representation of one slow query execution,
// used by both the profiler and getLog code paths.
type slowQueryEntry struct {
	EndTime        time.Time
	NS             string
	Op             string
	Millis         int64
	CPUNanos       int64
	ResponseLength int64
	KeysExamined   int64
	DocsExamined   int64
	NReturned      int64
	PlanSummary    string
	Command        bson.D
}

// structuredLogLine is the JSON shape of a MongoDB structured log entry.
type structuredLogLine struct {
	T    json.RawMessage `json:"t"`
	Msg  string          `json:"msg"`
	Attr struct {
		NS             string          `json:"ns"`
		Type           string          `json:"type"`
		DurationMillis int64           `json:"durationMillis"`
		CPUNanos       int64           `json:"cpuNanos"`
		Reslen         int64           `json:"reslen"`
		KeysExamined   int64           `json:"keysExamined"`
		DocsExamined   int64           `json:"docsExamined"`
		NReturned      int64           `json:"nreturned"`
		PlanSummary    string          `json:"planSummary"`
		Command        json.RawMessage `json:"command"`
	} `json:"attr"`
}

// topN sorts entries by Millis descending and returns the top n.
// Negative n is rejected by config validation and never reaches here.
func topN(entries []slowQueryEntry, n int) []slowQueryEntry {
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Millis > entries[j].Millis
	})
	if n == 0 || len(entries) == 0 {
		return nil
	}
	if len(entries) > n {
		return entries[:n]
	}
	return entries
}

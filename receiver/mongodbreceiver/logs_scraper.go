// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package mongodbreceiver // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/mongodbreceiver"

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.uber.org/zap"

	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/mongodbreceiver/internal/metadata"
)

const queryPlanSentinel = "__unexplainable__"

var systemDatabases = map[string]struct{}{
	"admin":    {},
	"local":    {},
	"config":   {},
	"__system": {},
}

// scrapeTopQueryLogs is the entry point for top_query log collection.
func (s *mongodbScraper) scrapeTopQueryLogs(ctx context.Context) (plog.Logs, error) {
	if interval := s.config.TopQueryCollection.CollectionInterval; interval > 0 {
		if !s.lastTopQueryExecution.IsZero() && time.Since(s.lastTopQueryExecution) < interval {
			s.logger.Debug("Skipping top_query collection, interval has not elapsed")
			return plog.NewLogs(), nil
		}
	}

	now := pcommon.NewTimestampFromTime(time.Now())

	serverStatus, err := s.client.ServerStatus(ctx, "admin")
	if err != nil {
		s.logger.Debug("Failed to get server status for top_query logs", zap.Error(err))
		return plog.NewLogs(), fmt.Errorf("failed to get server status for top_query logs: %w", err)
	}

	serverAddress, serverPort, err := serverAddressAndPort(serverStatus)
	if err != nil {
		s.logger.Debug("Failed to extract server address and port for top_query logs", zap.Error(err))
		return plog.NewLogs(), fmt.Errorf("failed to extract server address and port for top_query logs: %w", err)
	}

	// Use the server's clock as the reference time to avoid collector/server clock skew.
	// Fall back to the collector's wall clock if localTime is absent or of an unexpected type.
	serverNow := time.Now().UTC()
	if t, ok := serverStatus["localTime"].(bson.DateTime); ok {
		serverNow = t.Time().UTC()
	}

	dbNames, err := s.client.ListDatabaseNames(ctx, bson.M{})
	if err != nil {
		return plog.NewLogs(), fmt.Errorf("failed to list databases: %w", err)
	}

	var allEntries []slowQueryEntry
	logFallbackDBs := make(map[string]struct{})

	for _, dbName := range dbNames {
		if _, skip := systemDatabases[dbName]; skip {
			continue
		}
		entries, used, err := s.scrapeTopQueryFromProfiler(ctx, dbName, serverNow)
		if err != nil {
			s.logger.Warn("profiler scrape failed, will attempt getLog fallback",
				zap.String("db", dbName), zap.Error(err))
		}
		allEntries = append(allEntries, entries...)
		if !used {
			logFallbackDBs[dbName] = struct{}{}
		}
	}

	if len(logFallbackDBs) > 0 {
		entries, err := s.scrapeTopQueryFromGetLog(ctx, logFallbackDBs, serverNow)
		if err != nil {
			s.logger.Warn("getLog fallback failed", zap.Error(err))
		}
		allEntries = append(allEntries, entries...)
	}

	if len(allEntries) > 0 {
		s.processTopQueryEntries(ctx, allEntries, now)
	}

	rb := s.tlb.NewResourceBuilder()
	rb.SetServerAddress(serverAddress)
	rb.SetServerPort(serverPort)
	rb.SetServiceInstanceID(generateInstanceID(serverAddress, serverPort))
	s.tlb.EmitForResource(metadata.WithLogsResource(rb.Emit()))

	// Update the gate timestamp only after work completes successfully so that
	// a transient failure does not suppress retries for the full CollectionInterval.
	s.lastTopQueryExecution = time.Now()

	return s.tlb.Emit(), nil
}

// scrapeTopQueryFromProfiler collects slow query entries from system.profile for one database.
func (s *mongodbScraper) scrapeTopQueryFromProfiler(ctx context.Context, dbName string, serverNow time.Time) ([]slowQueryEntry, bool, error) {
	level, err := s.getProfilingLevel(ctx, dbName)
	if err != nil {
		return nil, false, fmt.Errorf("failed to get profiling level: %w", err)
	}
	if level == 0 {
		s.logger.Debug("profiler disabled, will use getLog fallback", zap.String("db", dbName))
		return nil, false, nil
	}

	lastScrape, seen := s.lastScrapeTime[dbName]
	var sinceTime time.Time
	if !seen {
		lookback := s.config.TopQueryCollection.LookbackWindow
		if lookback <= 0 {
			lookback = defaultLookbackWindow
		}
		sinceTime = serverNow.Add(-lookback)
	} else {
		sinceTime = lastScrape
	}

	maxRows := s.config.TopQueryCollection.MaxRowsPerQuery
	if maxRows <= 0 {
		maxRows = defaultTopMaxRowsPerQuery
	}
	filter := bson.D{{Key: "ts", Value: bson.D{{Key: "$gt", Value: sinceTime}}}}
	docs, err := s.client.FindProfileDocs(ctx, dbName, filter, maxRows)
	if err != nil {
		return nil, false, fmt.Errorf("failed to fetch profile docs: %w", err)
	}

	if len(docs) > 0 {
		// Advance cursor to the last document's ts.
		s.lastScrapeTime[dbName] = docs[len(docs)-1].TS
	} else {
		s.lastScrapeTime[dbName] = serverNow
	}

	entries := make([]slowQueryEntry, 0, len(docs))
	for _, d := range docs {
		if len(d.Command) == 0 {
			continue
		}
		entries = append(entries, slowQueryEntry{
			EndTime:        d.TS,
			NS:             d.NS,
			Op:             d.Op,
			Millis:         d.Millis,
			CPUNanos:       d.CPUNanos,
			ResponseLength: d.ResponseLength,
			KeysExamined:   d.KeysExamined,
			DocsExamined:   d.DocsExamined,
			NReturned:      d.NReturned,
			PlanSummary:    d.PlanSummary,
			Command:        d.Command,
		})
	}
	return entries, true, nil
}

func (s *mongodbScraper) getProfilingLevel(ctx context.Context, dbName string) (int, error) {
	result, err := s.client.RunCommand(ctx, dbName, bson.M{"profile": -1})
	if err != nil {
		return 0, err
	}
	was, ok := result["was"]
	if !ok {
		return 0, nil
	}
	switch v := was.(type) {
	case int32:
		return int(v), nil
	case int64:
		return int(v), nil
	case float64:
		return int(v), nil
	}
	return 0, nil
}

// scrapeTopQueryFromGetLog collects slow query entries from the getLog ring buffer.
// The ring buffer is ordered oldest→newest. We iterate in reverse (newest→oldest) and
// break as soon as EndTime <= sinceTime — everything before that point is guaranteed
// older, so we skip scanning the full 1024-entry buffer on every scrape.
func (s *mongodbScraper) scrapeTopQueryFromGetLog(ctx context.Context, dbSet map[string]struct{}, serverNow time.Time) ([]slowQueryEntry, error) {
	arr, err := s.client.GetLog(ctx)
	if err != nil {
		return nil, fmt.Errorf("getLog failed: %w", err)
	}

	sinceTime := s.lastGetLogTime
	if sinceTime.IsZero() {
		lookback := s.config.TopQueryCollection.LookbackWindow
		if lookback <= 0 {
			lookback = defaultLookbackWindow
		}
		sinceTime = serverNow.Add(-lookback)
	}

	var entries []slowQueryEntry
	var maxT time.Time

	// Iterate in reverse: newest entries first. Break as soon as we reach an entry
	// older than sinceTime — the ring buffer is in chronological order so all
	// remaining entries are also older.
	for i := len(arr) - 1; i >= 0; i-- {
		line, ok := arr[i].(string)
		if !ok {
			continue
		}
		entry, ok := parseLogLine(line)
		if !ok {
			continue
		}
		if !entry.EndTime.After(sinceTime) {
			break // all remaining entries are older — stop scanning
		}
		if _, want := dbSet[getDBFromNamespace(entry.NS)]; !want {
			continue
		}
		entries = append(entries, entry)
		if entry.EndTime.After(maxT) {
			maxT = entry.EndTime
		}
	}

	if !maxT.IsZero() {
		s.lastGetLogTime = maxT
	} else {
		s.lastGetLogTime = serverNow
	}

	return entries, nil
}

func parseLogLine(line string) (slowQueryEntry, bool) {
	var raw structuredLogLine
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return slowQueryEntry{}, false
	}
	if !strings.EqualFold(raw.Msg, "slow query") {
		return slowQueryEntry{}, false
	}
	if len(raw.Attr.Command) == 0 {
		return slowQueryEntry{}, false
	}

	// Decode "t": MongoDB encodes it as {"$date":"2026-01-02T03:04:05Z"} (relaxed Extended JSON).
	// bson.UnmarshalExtJSON in relaxed mode returns $date as a Go string; parse it as RFC3339.
	var tDoc bson.M
	if err := bson.UnmarshalExtJSON(raw.T, false, &tDoc); err != nil {
		return slowQueryEntry{}, false
	}
	dateStr, ok := tDoc["$date"].(string)
	if !ok {
		return slowQueryEntry{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, dateStr)
	if err != nil {
		return slowQueryEntry{}, false
	}

	var cmd bson.D
	if err := bson.UnmarshalExtJSON(raw.Attr.Command, false, &cmd); err != nil || len(cmd) == 0 {
		return slowQueryEntry{}, false
	}

	return slowQueryEntry{
		EndTime:        t,
		NS:             raw.Attr.NS,
		Op:             raw.Attr.Type,
		Millis:         raw.Attr.DurationMillis,
		CPUNanos:       raw.Attr.CPUNanos,
		ResponseLength: raw.Attr.Reslen,
		KeysExamined:   raw.Attr.KeysExamined,
		DocsExamined:   raw.Attr.DocsExamined,
		NReturned:      raw.Attr.NReturned,
		PlanSummary:    raw.Attr.PlanSummary,
		Command:        cmd,
	}, true
}

func (s *mongodbScraper) processTopQueryEntries(ctx context.Context, entries []slowQueryEntry, now pcommon.Timestamp) {
	ranked := topN(entries, int(s.config.TopQueryCollection.TopQueryCount))

	const msPerSec = 1000.0
	for i := range ranked {
		e := &ranked[i]

		obfuscated := s.obfuscator.obfuscateCommand(cleanCommand(e.Command))
		sig := querySignature(obfuscated)

		queryPlan, queryPlanHash := s.retrieveQueryPlan(ctx, e, sig)

		s.tlb.RecordDbServerTopQueryEvent(
			ctx,
			now,
			getCollectionFromNamespace(e.NS), // db.collection.name
			e.NS,                             // db.namespace
			obfuscated,                       // db.query.text
			metadata.AttributeDbSystemNameMongodb,
			float64(e.CPUNanos)/float64(time.Second), // mongodb.operation.cpu_time (ns → s)
			e.DocsExamined,             // mongodb.operation.docs_examined
			e.NReturned,                // mongodb.operation.docs_returned
			float64(e.Millis)/msPerSec, // mongodb.operation.duration (ms → s)
			queryPlan,                  // mongodb.operation.explain_plan
			queryPlanHash,              // mongodb.operation.explain_plan_hash
			e.KeysExamined,             // mongodb.operation.keys_examined
			e.PlanSummary,              // mongodb.operation.plan.summary
			e.ResponseLength,           // mongodb.operation.response_length
			e.Op,                       // mongodb.operation.type
		)
	}
}

func (s *mongodbScraper) retrieveQueryPlan(ctx context.Context, e *slowQueryEntry, sig string) (string, string) {
	if len(e.Command) == 0 {
		return "", ""
	}

	entryDB := getDBFromNamespace(e.NS)
	if entryDB == "" {
		return "", ""
	}

	cacheKey := e.NS + "|" + sig

	// Check cache if enabled (QueryPlanCacheSize > 0).
	if s.planCache != nil {
		if cached, ok := s.planCache.Get(cacheKey); ok {
			if cached == queryPlanSentinel {
				return "", ""
			}
			plan, hash, _ := strings.Cut(cached, "\x00")
			return plan, hash
		}
	}

	// For op=update and op=remove, system.profile stores the wire-protocol spec
	// {q, u, multi, upsert} or {q, limit} rather than the full command form.
	// Reconstruct into explain-compatible {"update"/"delete": collection, "updates"/"deletes": [...]}
	// before cleaning and wrapping.
	collection := getCollectionFromNamespace(e.NS)
	rawCmd := e.Command
	switch e.Op {
	case "update":
		rawCmd = reconstructUpdateForExplain(e.Command, collection)
	case "remove":
		rawCmd = reconstructDeleteForExplain(e.Command, collection)
	}

	// Clean the command once and pass to prepareForExplainCleaned so it is not cleaned twice.
	cmd := stripKeys(rawCmd, commandKeysToStrip)
	if !isExplainable(cmd) {
		if s.planCache != nil {
			s.planCache.Add(cacheKey, queryPlanSentinel)
		}
		return "", ""
	}

	prepared := prepareForExplainCleaned(cmd)
	result, err := s.client.RunCommand(ctx, entryDB, bson.D{
		{Key: "explain", Value: prepared},
		{Key: "verbosity", Value: "queryPlanner"},
	})
	if err != nil {
		s.logger.Warn("Failed to run explain", zap.String("db", entryDB), zap.Error(err))
		return "", ""
	}

	cleaned := cleanExplainResult(result)
	obfuscatedPlan := obfuscateExplainPlan(cleaned)
	plan := marshalJSON(obfuscatedPlan)
	planHash := querySignature(plan)

	if s.planCache != nil {
		s.planCache.Add(cacheKey, plan+"\x00"+planHash)
	}
	return plan, planHash
}

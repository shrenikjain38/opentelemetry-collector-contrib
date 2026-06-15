// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package mongodbreceiver

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/receiver/receivertest"

	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/mongodbreceiver/internal/metadata"
)

func newTopQueryTestScraper(t *testing.T, fc *fakeClient) *mongodbScraper {
	t.Helper()
	cfg := createDefaultConfig().(*Config)
	cfg.LogsBuilderConfig.Events.DbServerTopQuery.Enabled = true
	cfg.TopQueryCollection.QueryPlanCacheTTL = 0
	s := newMongodbScraper(receivertest.NewNopSettings(metadata.Type), cfg)
	s.client = fc
	return s
}

func TestParseLogLine_SlowQuery(t *testing.T) {
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	// Real MongoDB 4.4+ encodes "t" as Extended JSON {"$date":"..."}.
	line := fmt.Sprintf(
		`{"t":{"$date":"%s"},"msg":"Slow query","attr":{"ns":"appdb.users","type":"command","durationMillis":125,"cpuNanos":1000000,"reslen":42,"keysExamined":2,"docsExamined":3,"nreturned":1,"command":{"find":"users","filter":{"age":25}}}}`,
		ts.UTC().Format(time.RFC3339Nano),
	)

	entry, ok := parseLogLine(line)
	require.True(t, ok)
	require.Equal(t, "appdb.users", entry.NS)
	require.Equal(t, "command", entry.Op)
	require.Equal(t, int64(125), entry.Millis)
	require.Equal(t, int64(1_000_000), entry.CPUNanos)
	require.Equal(t, int64(42), entry.ResponseLength)
	require.Equal(t, int64(2), entry.KeysExamined)
	require.Equal(t, int64(3), entry.DocsExamined)
	require.Equal(t, int64(1), entry.NReturned)
	require.NotEmpty(t, entry.Command)
	require.True(t, entry.EndTime.Equal(ts))
}

func TestParseLogLine_NotSlowQuery(t *testing.T) {
	_, ok := parseLogLine(`{"t":"2026-01-02T03:04:05Z","msg":"Connection ended"}`)
	require.False(t, ok)
}

func TestParseLogLine_BadJSON(t *testing.T) {
	_, ok := parseLogLine("not json")
	require.False(t, ok)
}

func TestParseLogLine_MissingCommand(t *testing.T) {
	_, ok := parseLogLine(`{"t":"2026-01-02T03:04:05Z","msg":"Slow query","attr":{"ns":"x.y","durationMillis":1}}`)
	require.False(t, ok)
}

func TestGetProfilingLevel(t *testing.T) {
	cases := []struct {
		desc string
		ret  bson.M
		want int
	}{
		{"int32 was=1", bson.M{"was": int32(1)}, 1},
		{"int64 was=2", bson.M{"was": int64(2)}, 2},
		{"float64 was=0", bson.M{"was": float64(0)}, 0},
		{"missing was", bson.M{"ok": float64(1)}, 0},
		{"unknown type", bson.M{"was": "1"}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			fc := &fakeClient{}
			fc.On("RunCommand", mock.Anything, "appdb", bson.M{"profile": -1}).Return(tc.ret, nil)
			s := newTopQueryTestScraper(t, fc)
			level, err := s.getProfilingLevel(context.Background(), "appdb")
			require.NoError(t, err)
			require.Equal(t, tc.want, level)
		})
	}
}

func TestGetProfilingLevel_Error(t *testing.T) {
	fc := &fakeClient{}
	fc.On("RunCommand", mock.Anything, "appdb", bson.M{"profile": -1}).Return(bson.M(nil), errors.New("auth failed"))
	s := newTopQueryTestScraper(t, fc)
	_, err := s.getProfilingLevel(context.Background(), "appdb")
	require.Error(t, err)
}

func TestScrapeTopQueryFromProfiler_DisabledFallsBack(t *testing.T) {
	fc := &fakeClient{}
	fc.On("RunCommand", mock.Anything, "appdb", bson.M{"profile": -1}).Return(bson.M{"was": int32(0)}, nil)
	s := newTopQueryTestScraper(t, fc)
	_, used, err := s.scrapeTopQueryFromProfiler(context.Background(), "appdb", time.Now().UTC())
	require.NoError(t, err)
	require.False(t, used)
}

func TestScrapeTopQueryFromProfiler_AdvancesCursor(t *testing.T) {
	fc := &fakeClient{}
	fc.On("RunCommand", mock.Anything, "appdb", bson.M{"profile": -1}).Return(bson.M{"was": int32(1)}, nil)

	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	docs := []profileDoc{
		{
			TS: ts,
			NS:        "appdb.users",
			Op:        "insert",
			Millis:    100,
			Command:   bson.D{{Key: "insert", Value: "users"}, {Key: "documents", Value: bson.A{bson.D{{Key: "a", Value: 1}}}}},
		},
	}
	fc.On("FindProfileDocs", mock.Anything, "appdb", mock.Anything, mock.Anything).Return(docs, nil)

	s := newTopQueryTestScraper(t, fc)
	entries, used, err := s.scrapeTopQueryFromProfiler(context.Background(), "appdb", time.Now().UTC())
	require.NoError(t, err)
	require.True(t, used)
	require.Len(t, entries, 1)
	// Cursor advances to last doc's ts which is the query end time.
	require.True(t, s.lastScrapeTime["appdb"].Equal(ts))
}

func TestScrapeTopQueryFromGetLog_FiltersByDB(t *testing.T) {
	mkLine := func(ns string, ms int) string {
		return fmt.Sprintf(
			`{"t":{"$date":"%s"},"msg":"Slow query","attr":{"ns":"%s","type":"command","durationMillis":%d,"command":{"insert":"x","documents":[{"a":1}]}}}`,
			time.Now().UTC().Format(time.RFC3339Nano), ns, ms,
		)
	}

	fc := &fakeClient{}
	fc.On("GetLog", mock.Anything).Return(bson.A{
		mkLine("appdb.users", 100),
		mkLine("otherdb.things", 200),
		`{"msg":"not slow"}`,
	}, nil)

	s := newTopQueryTestScraper(t, fc)
	entries, err := s.scrapeTopQueryFromGetLog(context.Background(), map[string]struct{}{"appdb": {}}, time.Now().UTC())
	require.NoError(t, err)
	require.NotEmpty(t, entries)
	require.False(t, s.lastGetLogTime.IsZero())
}

func TestRetrieveQueryPlan_NotExplainable(t *testing.T) {
	fc := &fakeClient{}
	s := newTopQueryTestScraper(t, fc)

	e := &slowQueryEntry{
		NS:      "appdb.users",
		Op:      "insert",
		Command: bson.D{{Key: "insert", Value: "users"}},
	}
	sig := querySignature(s.obfuscator.obfuscateCommand(e.Command))
	plan, planHash := s.retrieveQueryPlan(context.Background(), e, sig)
	require.Empty(t, plan)
	require.Empty(t, planHash)

	if s.planCache != nil {
		v, ok := s.planCache.Get("appdb.users|" + sig)
		require.True(t, ok)
		require.Equal(t, queryPlanSentinel, v)
	}
}

func TestRetrieveQueryPlan_NoCacheStillRunsExplain(t *testing.T) {
	fc := &fakeClient{}
	fc.On("RunCommand", mock.Anything, "appdb", mock.Anything).Return(bson.M{
		"queryPlanner": bson.M{"winningPlan": bson.M{"stage": "COLLSCAN"}},
		"ok":           float64(1),
	}, nil)

	// QueryPlanCacheSize=0 → planCache is nil → explain still runs, results not cached
	cfg := createDefaultConfig().(*Config)
	cfg.LogsBuilderConfig.Events.DbServerTopQuery.Enabled = true
	cfg.TopQueryCollection.QueryPlanCacheTTL = 0
	cfg.TopQueryCollection.QueryPlanCacheSize = 0 // disable caching, not explain
	s := newMongodbScraper(receivertest.NewNopSettings(metadata.Type), cfg)
	s.client = fc
	require.Nil(t, s.planCache)

	e := &slowQueryEntry{
		NS:      "appdb.users",
		Op:      "query",
		Command: bson.D{{Key: "find", Value: "users"}, {Key: "filter", Value: bson.D{{Key: "x", Value: 1}}}},
	}
	sig := querySignature(s.obfuscator.obfuscateCommand(e.Command))

	plan, planHash := s.retrieveQueryPlan(context.Background(), e, sig)
	require.NotEmpty(t, plan, "explain should run even when cache is disabled")
	require.NotEmpty(t, planHash)

	// Second call should run explain again since there is no cache
	s.retrieveQueryPlan(context.Background(), e, sig)
	fc.AssertNumberOfCalls(t, "RunCommand", 2)
}

func TestRetrieveQueryPlan_CachesSuccess(t *testing.T) {
	fc := &fakeClient{}
	fc.On("RunCommand", mock.Anything, "appdb", mock.Anything).Return(bson.M{
		"queryPlanner": bson.M{"winningPlan": bson.M{"stage": "COLLSCAN"}},
		"ok":           float64(1),
	}, nil)

	s := newTopQueryTestScraper(t, fc)
	e := &slowQueryEntry{
		NS:      "appdb.users",
		Op:      "query",
		Command: bson.D{{Key: "find", Value: "users"}, {Key: "filter", Value: bson.D{{Key: "x", Value: 1}}}},
	}
	sig := querySignature(s.obfuscator.obfuscateCommand(e.Command))

	plan, planHash := s.retrieveQueryPlan(context.Background(), e, sig)
	require.NotEmpty(t, plan)
	require.NotEmpty(t, planHash)

	plan2, planHash2 := s.retrieveQueryPlan(context.Background(), e, sig)
	require.Equal(t, plan, plan2)
	require.Equal(t, planHash, planHash2)
	fc.AssertNumberOfCalls(t, "RunCommand", 1)
}

// T1: serverNow anchors first-scrape lookback in the server's clock domain.
// The filter sent to FindProfileDocs must use serverNow-lookback, not time.Now()-lookback.
// If the profiler returns no docs, lastScrapeTime[db] must be set to serverNow.
func TestScrapeTopQueryFromProfiler_FirstScrapeUsesServerNow(t *testing.T) {
	fc := &fakeClient{}
	fc.On("RunCommand", mock.Anything, "appdb", bson.M{"profile": -1}).Return(bson.M{"was": int32(1)}, nil)
	fc.On("FindProfileDocs", mock.Anything, "appdb", mock.Anything, mock.Anything).Return([]profileDoc{}, nil)

	s := newTopQueryTestScraper(t, fc)

	// Use a server time significantly different from the collector's wall clock
	// (1 hour in the past) to prove the filter is anchored to serverNow, not time.Now().
	serverNow := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

	_, _, err := s.scrapeTopQueryFromProfiler(context.Background(), "appdb", serverNow)
	require.NoError(t, err)

	// Cursor must be set to serverNow, not collector's wall clock.
	require.True(t, s.lastScrapeTime["appdb"].Equal(serverNow),
		"cursor should be serverNow=%v, got %v", serverNow, s.lastScrapeTime["appdb"])

	// The filter passed to FindProfileDocs must use serverNow-lookback.
	// Extract the actual filter argument from the mock call.
	call := fc.Calls[1] // second call is FindProfileDocs
	filterArg := call.Arguments.Get(2).(bson.D)
	require.Equal(t, "ts", filterArg[0].Key)
	filterCondition := filterArg[0].Value.(bson.D)
	require.Equal(t, "$gt", filterCondition[0].Key)
	sinceTime := filterCondition[0].Value.(time.Time)

	// sinceTime must be serverNow - lookback, not time.Now() - lookback.
	// If it were anchored to collector clock (time.Now()), it would be ~current time,
	// not ~2020. The difference must be within the lookback window of serverNow.
	lookback := s.config.TopQueryCollection.LookbackWindow
	if lookback <= 0 {
		lookback = defaultLookbackWindow
	}
	expectedSinceTime := serverNow.Add(-lookback)
	require.True(t, sinceTime.Equal(expectedSinceTime),
		"filter sinceTime should be serverNow-lookback=%v, got %v (if this equals ~time.Now()-lookback the clock skew fix is broken)",
		expectedSinceTime, sinceTime)
}

// T3: empty-command entries must not consume topN slots.
// With TopQueryCount=2, if the 2 slowest entries have empty commands, they must not
// Empty-command filtering now happens at the collection site (scrapeTopQueryFromProfiler),
// not in processTopQueryEntries. This test verifies that the profiler path correctly
// filters empty-command docs before they reach topN ranking.
func TestScrapeTopQueryFromProfiler_FiltersEmptyCommandDocs(t *testing.T) {
	fc := &fakeClient{}
	fc.On("RunCommand", mock.Anything, "appdb", bson.M{"profile": -1}).Return(bson.M{"was": int32(1)}, nil)

	ts := time.Now().UTC()
	docs := []profileDoc{
		// Empty command — must be filtered out
		{TS: ts, NS: "appdb.a", Op: "query", Millis: 500, Command: bson.D{}},
		{TS: ts, NS: "appdb.b", Op: "query", Millis: 400, Command: bson.D{}},
		// Valid commands — must survive
		{TS: ts, NS: "appdb.users", Op: "insert", Millis: 300,
			Command: bson.D{{Key: "insert", Value: "users"}, {Key: "documents", Value: bson.A{bson.D{{Key: "a", Value: 1}}}}}},
		{TS: ts, NS: "appdb.orders", Op: "insert", Millis: 200,
			Command: bson.D{{Key: "insert", Value: "orders"}, {Key: "documents", Value: bson.A{bson.D{{Key: "b", Value: 2}}}}}},
	}
	fc.On("FindProfileDocs", mock.Anything, "appdb", mock.Anything, mock.Anything).Return(docs, nil)

	s := newTopQueryTestScraper(t, fc)
	entries, used, err := s.scrapeTopQueryFromProfiler(context.Background(), "appdb", time.Now().UTC())
	require.NoError(t, err)
	require.True(t, used)

	// Only 2 valid-command entries must be returned; the 2 empty-command docs are dropped.
	require.Len(t, entries, 2)
	require.Equal(t, "appdb.users", entries[0].NS)
	require.Equal(t, "appdb.orders", entries[1].NS)
}

// T5: sentinel cache must prevent a second RunCommand for unexplainable commands.
func TestRetrieveQueryPlan_SentinelPreventsDuplicateExplain(t *testing.T) {
	fc := &fakeClient{}
	// RunCommand should never be called for unexplainable commands
	s := newTopQueryTestScraper(t, fc)

	e := &slowQueryEntry{
		NS:      "appdb.users",
		Op:      "insert",
		Command: bson.D{{Key: "insert", Value: "users"}},
	}
	sig := querySignature(s.obfuscator.obfuscateCommand(e.Command))

	// First call: isExplainable=false → sentinel written, "", "" returned
	plan1, hash1 := s.retrieveQueryPlan(context.Background(), e, sig)
	require.Empty(t, plan1)
	require.Empty(t, hash1)

	// Second call: sentinel is in cache → must return immediately without RunCommand
	plan2, hash2 := s.retrieveQueryPlan(context.Background(), e, sig)
	require.Empty(t, plan2)
	require.Empty(t, hash2)

	// RunCommand must never have been called — sentinel prevents the explain attempt
	fc.AssertNotCalled(t, "RunCommand")

	// Verify sentinel is actually stored
	if s.planCache != nil {
		v, ok := s.planCache.Get("appdb.users|" + sig)
		require.True(t, ok, "sentinel must be in cache")
		require.Equal(t, queryPlanSentinel, v)
	}
}

// T7: end-to-end scrapeTopQueryLogs covering both profiler path (one DB)
// and getLog fallback path (another DB) in the same scrape.
// Verifies entries from both sources are combined and emitted.
func TestScrapeTopQueryLogs_CombinesProfilerAndGetLogEntries(t *testing.T) {
	fc := &fakeClient{}

	// ServerStatus returns a valid host and localTime
	fc.On("ServerStatus", mock.Anything, "admin").Return(bson.M{
		"host":      "mongo1:27017",
		"localTime": bson.DateTime(time.Now().UnixMilli()),
	}, nil)

	// Two databases returned
	fc.On("ListDatabaseNames", mock.Anything, mock.Anything, mock.Anything).Return(
		[]string{"profilerdb", "getlogdb"}, nil,
	)

	// profilerdb: profiler enabled → profiler path
	fc.On("RunCommand", mock.Anything, "profilerdb", bson.M{"profile": -1}).
		Return(bson.M{"was": int32(1)}, nil)
	// Use insert (unexplainable) so retrieveQueryPlan doesn't issue an explain RunCommand,
	// keeping this test focused on the profiler+getLog combination, not plan fetching.
	profilerDoc := profileDoc{
		TS: time.Now().Add(-500 * time.Millisecond),
		NS:        "profilerdb.users",
		Op:        "insert",
		Millis:    200,
		Command:   bson.D{{Key: "insert", Value: "users"}, {Key: "documents", Value: bson.A{bson.D{{Key: "a", Value: 1}}}}},
	}
	fc.On("FindProfileDocs", mock.Anything, "profilerdb", mock.Anything, mock.Anything).
		Return([]profileDoc{profilerDoc}, nil)

	// getlogdb: profiler disabled → getLog fallback
	fc.On("RunCommand", mock.Anything, "getlogdb", bson.M{"profile": -1}).
		Return(bson.M{"was": int32(0)}, nil)

	// getLog returns one slow query for getlogdb
	now := time.Now().UTC()
	getlogLine := fmt.Sprintf(
		`{"t":{"$date":"%s"},"msg":"Slow query","attr":{"ns":"getlogdb.orders","type":"command","durationMillis":300,"command":{"insert":"orders","documents":[{"a":1}]}}}`,
		now.Format(time.RFC3339Nano),
	)
	fc.On("GetLog", mock.Anything).Return(bson.A{getlogLine}, nil)

	cfg := createDefaultConfig().(*Config)
	cfg.LogsBuilderConfig.Events.DbServerTopQuery.Enabled = true
	cfg.TopQueryCollection.QueryPlanCacheTTL = 0
	cfg.TopQueryCollection.TopQueryCount = 10
	cfg.TopQueryCollection.CollectionInterval = 0 // no self-gate
	s := newMongodbScraper(receivertest.NewNopSettings(metadata.Type), cfg)
	s.client = fc

	logs, err := s.scrapeTopQueryLogs(context.Background())
	require.NoError(t, err)

	require.Equal(t, 1, logs.ResourceLogs().Len())
	sl := logs.ResourceLogs().At(0).ScopeLogs().At(0)

	// Must have 2 events: 1 from profiler (profilerdb.users) + 1 from getLog (getlogdb.orders)
	require.Equal(t, 2, sl.LogRecords().Len(),
		"expected 2 events: one from profiler path, one from getLog path")

	// Collect emitted namespaces — order is by Millis desc (300 > 200)
	var namespaces []string
	for i := 0; i < sl.LogRecords().Len(); i++ {
		v, ok := sl.LogRecords().At(i).Attributes().Get("db.namespace")
		require.True(t, ok)
		namespaces = append(namespaces, v.AsString())
	}
	require.ElementsMatch(t, []string{"profilerdb.users", "getlogdb.orders"}, namespaces,
		"entries from both profiler and getLog paths must be emitted")

	// Resource attributes must be set from ServerStatus
	ra := logs.ResourceLogs().At(0).Resource().Attributes()
	addr, ok := ra.Get("server.address")
	require.True(t, ok)
	require.Equal(t, "mongo1", addr.AsString())
}

func TestProcessTopQueryEntries_EmitsEvent(t *testing.T) {
	fc := &fakeClient{}
	s := newTopQueryTestScraper(t, fc)

	entries := []slowQueryEntry{
		{
			EndTime:        time.Now(),
			NS:             "appdb.users",
			Op:             "insert",
			Millis:         100,
			CPUNanos:       1_000_000,
			ResponseLength: 10,
			KeysExamined:   1,
			DocsExamined:   1,
			NReturned:      1,
			Command:        bson.D{{Key: "insert", Value: "users"}, {Key: "documents", Value: bson.A{bson.D{{Key: "a", Value: 1}}}}},
		},
	}

	now := pcommon.NewTimestampFromTime(time.Now())
	s.processTopQueryEntries(context.Background(), entries, now)

	rb := s.tlb.NewResourceBuilder()
	rb.SetServerAddress("localhost")
	rb.SetServerPort(27017)
	rb.SetServiceInstanceID(generateInstanceID("localhost", 27017))
	s.tlb.EmitForResource(metadata.WithLogsResource(rb.Emit()))

	logs := s.tlb.Emit()
	require.Equal(t, 1, logs.ResourceLogs().Len())
	sl := logs.ResourceLogs().At(0).ScopeLogs().At(0)
	require.Equal(t, 1, sl.LogRecords().Len())

	attrs := sl.LogRecords().At(0).Attributes()

	v, ok := attrs.Get("db.system.name")
	require.True(t, ok)
	require.Equal(t, "mongodb", v.AsString())

	v, ok = attrs.Get("db.namespace")
	require.True(t, ok)
	require.Equal(t, "appdb.users", v.AsString())

	v, ok = attrs.Get("mongodb.operation.type")
	require.True(t, ok)
	require.Equal(t, "insert", v.AsString())

	// query_sample aggregated fields must not be present
	_, hasCount := attrs.Get("mongodb.operation.count")
	require.False(t, hasCount)
}

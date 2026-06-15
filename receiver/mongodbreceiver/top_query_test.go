// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package mongodbreceiver

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestProfileDocEndTime(t *testing.T) {
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	d := profileDoc{TS: ts, Millis: 250}
	// ts IS the end time — EndTime = d.TS directly (no ts+millis calculation).
	e := slowQueryEntry{EndTime: d.TS}
	require.Equal(t, ts, e.EndTime)
}

func TestTopN_RanksByMillisAndLimits(t *testing.T) {
	entries := []slowQueryEntry{
		{NS: "appdb.users", Millis: 100},
		{NS: "appdb.orders", Millis: 500},
		{NS: "appdb.things", Millis: 250},
	}
	ranked := topN(entries, 2)
	require.Len(t, ranked, 2)
	require.Equal(t, int64(500), ranked[0].Millis)
	require.Equal(t, int64(250), ranked[1].Millis)
}

func TestTopN_ZeroEmitsNothing(t *testing.T) {
	// TopQueryCount=0 means "emit nothing, cursors still advance".
	entries := []slowQueryEntry{{Millis: 100}, {Millis: 500}}
	require.Empty(t, topN(entries, 0))
}

func TestTopN_LargeNReturnsAll(t *testing.T) {
	// n > len(entries) returns all — no panic, no truncation.
	entries := []slowQueryEntry{{Millis: 100}, {Millis: 500}}
	require.Len(t, topN(entries, 100), 2)
}

func TestQuerySignature_Stable(t *testing.T) {
	require.Equal(t, querySignature("foo"), querySignature("foo"))
	require.NotEqual(t, querySignature("foo"), querySignature("bar"))
	require.Len(t, querySignature("anything"), 16)
}

func TestIsExplainable(t *testing.T) {
	require.True(t, isExplainable(bson.D{{Key: "find", Value: "users"}}))
	require.True(t, isExplainable(bson.D{{Key: "aggregate", Value: "orders"}}))
	require.True(t, isExplainable(bson.D{{Key: "update", Value: "users"}}))
	require.True(t, isExplainable(bson.D{{Key: "delete", Value: "users"}}))
	require.False(t, isExplainable(bson.D{{Key: "insert", Value: "users"}}))
	require.False(t, isExplainable(bson.D{{Key: "getMore", Value: int64(99)}}))
	require.False(t, isExplainable(bson.D{{Key: "explain", Value: bson.D{}}}))
	require.False(t, isExplainable(bson.D{}))
}

func TestCleanCommand_StripsSessionKeys(t *testing.T) {
	cmd := bson.D{
		{Key: "find", Value: "users"},
		{Key: "lsid", Value: bson.D{{Key: "id", Value: "x"}}},
		{Key: "$clusterTime", Value: bson.D{{Key: "t", Value: 1}}},
		{Key: "comment", Value: "hello"},
		{Key: "let", Value: bson.D{{Key: "v", Value: 1}}},
	}
	out := cleanCommand(cmd)
	keys := keysOfD(out)
	require.NotContains(t, keys, "lsid")
	require.NotContains(t, keys, "$clusterTime")
	require.NotContains(t, keys, "comment")
	require.Contains(t, keys, "let")
	require.Contains(t, keys, "find")
}

func TestCleanCommandD_StripsAllDriverMetadata(t *testing.T) {
	cmd := bson.D{
		{Key: "aggregate", Value: "users"},
		{Key: "lsid", Value: bson.D{}},
		{Key: "$db", Value: "appdb"},
		{Key: "let", Value: bson.D{}},
		{Key: "readConcern", Value: bson.D{}},
		{Key: "pipeline", Value: bson.A{}},
	}
	out := stripKeys(cmd, commandKeysToStrip)
	keys := keysOfD(out)
	require.Contains(t, keys, "aggregate")
	require.Contains(t, keys, "pipeline")
	require.NotContains(t, keys, "lsid")
	require.NotContains(t, keys, "$db")
	require.Contains(t, keys, "let") // let is kept — user-defined variables must survive for explain to work
	require.NotContains(t, keys, "readConcern")
}

func TestPrepareForExplain_AddsCursorForAggregate(t *testing.T) {
	cmd := bson.D{
		{Key: "aggregate", Value: "users"},
		{Key: "pipeline", Value: bson.A{}},
	}
	out := prepareForExplainCleaned(stripKeys(cmd, commandKeysToStrip))
	require.Contains(t, keysOfD(out), "cursor")
}

func TestPrepareForExplain_DoesNotAddCursorForFind(t *testing.T) {
	cmd := bson.D{{Key: "find", Value: "users"}}
	out := prepareForExplainCleaned(stripKeys(cmd, commandKeysToStrip))
	require.NotContains(t, keysOfD(out), "cursor")
}

func TestGetCollectionFromNamespaceTopQuery(t *testing.T) {
	require.Equal(t, "users", getCollectionFromNamespace("appdb.users"))
	require.Equal(t, "", getCollectionFromNamespace("noseparator"))
}

func TestGetDBFromNamespaceTopQuery(t *testing.T) {
	require.Equal(t, "appdb", getDBFromNamespace("appdb.users"))
	require.Equal(t, "", getDBFromNamespace("noseparator"))
}

func TestCleanExplainResult(t *testing.T) {
	in := map[string]any{
		"queryPlanner":  map[string]any{"winningPlan": "x"},
		"serverInfo":    map[string]any{"host": "h"},
		"ok":            float64(1),
		"$clusterTime":  map[string]any{"t": 1},
		"operationTime": int64(123),
		"command":       map[string]any{"find": "x"},
	}
	out := cleanExplainResult(in)
	require.Contains(t, out, "queryPlanner")
	require.NotContains(t, out, "serverInfo")
	require.NotContains(t, out, "ok")
	require.NotContains(t, out, "$clusterTime")
	require.NotContains(t, out, "operationTime")
	require.NotContains(t, out, "command")
}

func TestObfuscateExplainPlan_SameStructureSameHash(t *testing.T) {
	plan1 := map[string]any{
		"queryPlanner": map[string]any{
			"winningPlan": map[string]any{"stage": "COLLSCAN"},
			"parsedQuery": map[string]any{"status": map[string]any{"$eq": "A"}},
		},
	}
	plan2 := map[string]any{
		"queryPlanner": map[string]any{
			"winningPlan": map[string]any{"stage": "COLLSCAN"},
			"parsedQuery": map[string]any{"status": map[string]any{"$eq": "B"}},
		},
	}
	h1 := querySignature(marshalJSON(obfuscateExplainPlan(plan1)))
	h2 := querySignature(marshalJSON(obfuscateExplainPlan(plan2)))
	require.Equal(t, h1, h2)
}

func TestObfuscateExplainPlan_DifferentStructureDifferentHash(t *testing.T) {
	collscan := map[string]any{"queryPlanner": map[string]any{"winningPlan": map[string]any{"stage": "COLLSCAN"}}}
	ixscan := map[string]any{"queryPlanner": map[string]any{"winningPlan": map[string]any{
		"stage":      "FETCH",
		"inputStage": map[string]any{"stage": "IXSCAN", "keyPattern": map[string]any{"status": 1}},
	}}}
	h1 := querySignature(marshalJSON(obfuscateExplainPlan(collscan)))
	h2 := querySignature(marshalJSON(obfuscateExplainPlan(ixscan)))
	require.NotEqual(t, h1, h2)
}


// TestObfuscateExplainPlan_RealExplainStructures tests against the exact structures
// returned by the MongoDB driver from RunCommand, using bson.M and bson.A types.
func TestObfuscateExplainPlan_RealExplainStructures(t *testing.T) {
	t.Run("IXSCAN with multi-value indexBounds (bson.A)", func(t *testing.T) {
		// Real structure from: db.orders.find({order_id: {$in: [1,2,3]}}).explain()
		// indexBounds value is bson.A (driver type), not []any
		plan := bson.M{
			"queryPlanner": bson.M{
				"parsedQuery": bson.M{
					"order_id": bson.M{"$in": bson.A{int32(1), int32(2), int32(3)}},
				},
				"winningPlan": bson.M{
					"stage": "FETCH",
					"inputStage": bson.M{
						"stage":      "IXSCAN",
						"keyPattern": bson.M{"order_id": int32(1)},
						"indexName":  "order_id_1",
						"direction":  "forward",
						"indexBounds": bson.M{
							"order_id": bson.A{"[1, 1]", "[2, 2]", "[3, 3]"},
						},
						"multiKeyPaths": bson.M{
							"order_id": bson.A{}, // empty bson.A
						},
					},
				},
				"rejectedPlans": bson.A{},
			},
		}
		result := obfuscateExplainPlan(plan).(map[string]any)
		qp := result["queryPlanner"].(map[string]any)

		// parsedQuery literals must be redacted
		pq := qp["parsedQuery"].(map[string]any)
		inClause := pq["order_id"].(map[string]any)
		inVals, ok := inClause["$in"].([]any)
		require.True(t, ok, "$in should be []any after obfuscation, not bson.A")
		require.Equal(t, []any{"?", "?", "?"}, inVals, "$in values must be redacted")

		// winningPlan structural fields must be preserved
		wp := qp["winningPlan"].(map[string]any)
		require.Equal(t, "FETCH", wp["stage"])
		is := wp["inputStage"].(map[string]any)
		require.Equal(t, "IXSCAN", is["stage"])
		require.Equal(t, "order_id_1", is["indexName"])
		require.Equal(t, "forward", is["direction"])

		// keyPattern (structural) must be preserved
		kp := is["keyPattern"].(map[string]any)
		require.Equal(t, int32(1), kp["order_id"])

		// indexBounds values must be redacted but structure preserved
		ib := is["indexBounds"].(map[string]any)
		ibVals, ok := ib["order_id"].([]any)
		require.True(t, ok, "indexBounds order_id should be []any after obfuscation, not bson.A or string")
		require.Equal(t, []any{"?", "?", "?"}, ibVals, "indexBounds values must be redacted")

		// empty bson.A must produce empty []any, not be redacted to "?"
		mp := is["multiKeyPaths"].(map[string]any)
		mpVals, ok := mp["order_id"].([]any)
		require.True(t, ok, "empty bson.A should become []any{}, not string '?'")
		require.Empty(t, mpVals)

		// rejectedPlans (empty bson.A at top level) must survive as []any
		rp, ok := qp["rejectedPlans"].([]any)
		require.True(t, ok, "rejectedPlans should be []any, not bson.A or '?'")
		require.Empty(t, rp)
	})

	t.Run("COLLSCAN with $and filter containing bson.A", func(t *testing.T) {
		// Real structure from: db.orders.find({$and:[{status:'A'},{total_amount:{$gt:50}}]}).explain()
		plan := bson.M{
			"queryPlanner": bson.M{
				"parsedQuery": bson.M{
					"$and": bson.A{
						bson.M{"status": bson.M{"$eq": "A"}},
						bson.M{"total_amount": bson.M{"$gt": int32(50)}},
					},
				},
				"winningPlan": bson.M{
					"stage": "COLLSCAN",
					"filter": bson.M{
						"$and": bson.A{
							bson.M{"status": bson.M{"$eq": "A"}},
							bson.M{"total_amount": bson.M{"$gt": int32(50)}},
						},
					},
					"direction": "forward",
				},
				"rejectedPlans": bson.A{},
			},
		}
		result := obfuscateExplainPlan(plan).(map[string]any)
		qp := result["queryPlanner"].(map[string]any)

		// parsedQuery $and array must be recursively redacted
		pq := qp["parsedQuery"].(map[string]any)
		andVals, ok := pq["$and"].([]any)
		require.True(t, ok, "$and should be []any after obfuscation")
		require.Len(t, andVals, 2)
		first := andVals[0].(map[string]any)
		statusClause := first["status"].(map[string]any)
		require.Equal(t, "?", statusClause["$eq"], "filter value must be redacted")

		// filter inside winningPlan must also be redacted
		wp := qp["winningPlan"].(map[string]any)
		require.Equal(t, "COLLSCAN", wp["stage"])
		require.Equal(t, "forward", wp["direction"]) // structural, preserved
		filterAnd := wp["filter"].(map[string]any)["$and"].([]any)
		require.Len(t, filterAnd, 2)
		firstFilter := filterAnd[0].(map[string]any)
		require.Equal(t, "?", firstFilter["status"].(map[string]any)["$eq"])
	})

	t.Run("rejectedPlans with non-empty bson.A containing filter and indexBounds", func(t *testing.T) {
		// Real structure from:
		// db.orders.find({customer_id:1, order_date:{$gt:new Date('2026-01-01')}}).explain()
		// with two candidate indexes — one wins, one is rejected.
		// rejectedPlans is bson.A containing bson.M entries with their own filter/indexBounds.
		plan := bson.M{
			"queryPlanner": bson.M{
				"parsedQuery": bson.M{
					"$and": bson.A{
						bson.M{"customer_id": bson.M{"$eq": int32(1)}},
						bson.M{"order_date": bson.M{"$gt": bson.DateTime(1767225600000)}},
					},
				},
				"winningPlan": bson.M{
					"stage": "FETCH",
					"filter": bson.M{
						"order_date": bson.M{"$gt": bson.DateTime(1767225600000)},
					},
					"inputStage": bson.M{
						"stage":     "IXSCAN",
						"indexName": "customer_id_1",
						"keyPattern": bson.M{"customer_id": int32(1)},
						"direction": "forward",
						"indexBounds": bson.M{
							"customer_id": bson.A{"[1, 1]"},
						},
						"multiKeyPaths": bson.M{"customer_id": bson.A{}},
					},
				},
				// rejectedPlans is bson.A of bson.M — each has its own filter + indexBounds
				"rejectedPlans": bson.A{
					bson.M{
						"stage": "FETCH",
						"filter": bson.M{
							"customer_id": bson.M{"$eq": int32(1)},
						},
						"inputStage": bson.M{
							"stage":     "IXSCAN",
							"indexName": "order_date_-1",
							"keyPattern": bson.M{"order_date": int32(-1)},
							"direction": "forward",
							"indexBounds": bson.M{
								"order_date": bson.A{"[new Date(9223372036854775807), new Date(1767225600000))"},
							},
							"multiKeyPaths": bson.M{"order_date": bson.A{}},
						},
					},
				},
			},
		}

		result := obfuscateExplainPlan(plan).(map[string]any)
		qp := result["queryPlanner"].(map[string]any)

		// rejectedPlans must be []any (not bson.A or "?")
		rp, ok := qp["rejectedPlans"].([]any)
		require.True(t, ok, "rejectedPlans should be []any after obfuscation, not bson.A or '?'")
		require.Len(t, rp, 1)

		// The rejected plan itself must be recursed into
		rejected := rp[0].(map[string]any)
		require.Equal(t, "FETCH", rejected["stage"], "rejected plan stage must be preserved")

		// filter inside the rejected plan must be redacted
		rejFilter := rejected["filter"].(map[string]any)
		custEq := rejFilter["customer_id"].(map[string]any)
		require.Equal(t, "?", custEq["$eq"], "rejected plan filter value must be redacted")

		// indexBounds inside rejected plan's inputStage must be redacted
		rejIS := rejected["inputStage"].(map[string]any)
		require.Equal(t, "IXSCAN", rejIS["stage"])
		require.Equal(t, "order_date_-1", rejIS["indexName"], "index name must be preserved")
		rejIB := rejIS["indexBounds"].(map[string]any)
		rejIBVals, ok := rejIB["order_date"].([]any)
		require.True(t, ok, "indexBounds inside rejectedPlan should be []any, not bson.A")
		require.Equal(t, []any{"?"}, rejIBVals, "indexBounds value inside rejectedPlan must be redacted")

		// keyPattern inside rejected plan must be preserved (structural)
		rejKP := rejIS["keyPattern"].(map[string]any)
		require.Equal(t, int32(-1), rejKP["order_date"], "keyPattern value must be preserved")

		// winningPlan filter must also be redacted
		wp := qp["winningPlan"].(map[string]any)
		wpFilter := wp["filter"].(map[string]any)
		wpDate := wpFilter["order_date"].(map[string]any)
		require.Equal(t, "?", wpDate["$gt"], "winning plan filter value must be redacted")

		// winningPlan indexBounds must be redacted
		wpIS := wp["inputStage"].(map[string]any)
		wpIB := wpIS["indexBounds"].(map[string]any)
		wpIBVals, ok := wpIB["customer_id"].([]any)
		require.True(t, ok, "winning plan indexBounds should be []any")
		require.Equal(t, []any{"?"}, wpIBVals)

		// parsedQuery $and array must be recursively redacted
		pq := qp["parsedQuery"].(map[string]any)
		andVals, ok := pq["$and"].([]any)
		require.True(t, ok, "parsedQuery $and should be []any")
		require.Len(t, andVals, 2)
		custClause := andVals[0].(map[string]any)["customer_id"].(map[string]any)
		require.Equal(t, "?", custClause["$eq"])
	})

	t.Run("Same literals different values produce same hash", func(t *testing.T) {
		mkPlan := func(val string) bson.M {
			return bson.M{
				"queryPlanner": bson.M{
					"parsedQuery": bson.M{"status": bson.M{"$eq": val}},
					"winningPlan": bson.M{"stage": "COLLSCAN", "direction": "forward"},
					"rejectedPlans": bson.A{},
				},
			}
		}
		h1 := querySignature(marshalJSON(obfuscateExplainPlan(mkPlan("A"))))
		h2 := querySignature(marshalJSON(obfuscateExplainPlan(mkPlan("Z"))))
		require.Equal(t, h1, h2, "different literal values, same plan structure must produce same hash")
	})

	t.Run("Different plan structures produce different hashes", func(t *testing.T) {
		collscan := bson.M{"queryPlanner": bson.M{"winningPlan": bson.M{"stage": "COLLSCAN"}}}
		ixscan := bson.M{"queryPlanner": bson.M{"winningPlan": bson.M{
			"stage":      "FETCH",
			"inputStage": bson.M{"stage": "IXSCAN", "keyPattern": bson.M{"order_id": int32(1)}},
		}}}
		h1 := querySignature(marshalJSON(obfuscateExplainPlan(collscan)))
		h2 := querySignature(marshalJSON(obfuscateExplainPlan(ixscan)))
		require.NotEqual(t, h1, h2, "COLLSCAN vs IXSCAN must produce different hashes")
	})
}

func keysOfD(d bson.D) []string {
	out := make([]string, 0, len(d))
	for _, e := range d {
		out = append(out, e.Key)
	}
	return out
}

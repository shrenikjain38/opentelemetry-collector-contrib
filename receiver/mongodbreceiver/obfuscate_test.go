// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package mongodbreceiver

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestObfuscateCommand(t *testing.T) {
	o := newObfuscator()

	command := bson.D{
		{Key: "find", Value: "users"},
		{Key: "filter", Value: bson.M{"name": "test", "age": 30}},
		{Key: "comment", Value: "test query"},
	}

	cleanedCommand := cleanCommand(command)
	obfuscated := o.obfuscateMongoDBString(cleanedCommand.String())
	require.Contains(t, obfuscated, "find")
	require.NotContains(t, obfuscated, "users")
	require.NotContains(t, obfuscated, "test")
	require.NotContains(t, obfuscated, "30")
}

func TestGenerateQuerySignature(t *testing.T) {
	query1 := `{"find":"users","filter":{"name":"test"}}`
	query2 := `{"find":"users","filter":{"name":"different"}}`

	sig1 := generateQuerySignature(query1)
	sig2 := generateQuerySignature(query2)
	sig1Again := generateQuerySignature(query1)

	require.Equal(t, sig1, sig1Again)
	require.NotEqual(t, sig1, sig2)
	require.Len(t, sig1, 16) // 8 bytes hex encoded
}

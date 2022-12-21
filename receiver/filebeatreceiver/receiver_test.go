// Copyright 2022, OpenTelemetry Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package filebeatreceiver

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/config"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/receiver/receivertest"
)

func Test_NewLogsReceiver(t *testing.T) {
	testCases := []struct {
		desc     string
		testFunc func(*testing.T)
	}{
		{
			desc: "creates a new LogsReceiver returns error with nil endpoint",
			testFunc: func(t *testing.T) {
				var cfg component.Config = &Config{
					ReceiverSettings: config.NewReceiverSettings(component.NewID(typeStr)),
				}
				_, err := createLogsReceiver(
					context.Background(),
					receivertest.NewNopCreateSettings(),
					cfg,
					consumertest.NewNop(),
				)
				require.ErrorIs(t, err, errEmptyEndpoint)
			},
		},
		{
			desc: "creates a new LogsReceiver returns no error",
			testFunc: func(t *testing.T) {
				cfg := createDefaultConfig()
				_, err := createLogsReceiver(
					context.Background(),
					receivertest.NewNopCreateSettings(),
					cfg,
					consumertest.NewNop(),
				)
				require.NoError(t, err)
			},
		},
		{
			desc: "creates a new LogsReceiver returns error for nil logs consumer",
			testFunc: func(t *testing.T) {
				cfg := createDefaultConfig()
				_, err := createLogsReceiver(
					context.Background(),
					receivertest.NewNopCreateSettings(),
					cfg,
					nil,
				)
				require.ErrorIs(t, err, errNilNextLogsConsumer)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, tc.testFunc)
	}
}

func Test_Server(t *testing.T) {
	cfg := createDefaultConfig()
	receiver, err1 := createLogsReceiver(
		context.Background(),
		receivertest.NewNopCreateSettings(),
		cfg,
		consumertest.NewNop(),
	)
	require.NoError(t, err1)
	err2 := receiver.Start(context.Background(), nil)
	require.NoError(t, err2)

	err3 := receiver.Shutdown(context.Background())
	require.NoError(t, err3)
}

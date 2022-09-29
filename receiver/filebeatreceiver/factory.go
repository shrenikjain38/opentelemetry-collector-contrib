// Copyright 2019, OpenTelemetry Authors
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

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/config"
	"go.opentelemetry.io/collector/consumer"
)

// This file implements factory for Filebeat receiver.

const (
	// The value of "type" key in configuration.
	typeStr = "filebeat"

	// Default endpoints to bind to.
	defaultEndpoint = ":5044"
)

// NewFactory creates a factory for Filebeat receiver.
func NewFactory() component.ReceiverFactory {
	return component.NewReceiverFactory(typeStr, createDefaultConfig, component.WithLogsReceiver(createLogsReceiver, component.StabilityLevelBeta))
}

// CreateDefaultConfig creates the default configuration for Filebeat receiver.
func createDefaultConfig() config.Receiver {
	return &Config{
		ReceiverSettings: config.NewReceiverSettings(config.NewComponentID(typeStr)),
		Endpoint:         defaultEndpoint,
	}
}

// createLogsReceiver creates a logs receiver based on provided config.
func createLogsReceiver(
	_ context.Context,
	params component.ReceiverCreateSettings,
	cfg config.Receiver,
	consumer consumer.Logs,
) (component.LogsReceiver, error) {

	rCfg := cfg.(*Config)
	return NewLogsReceiver(params.Logger, *rCfg, consumer)
}

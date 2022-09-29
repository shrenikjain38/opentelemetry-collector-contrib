// Copyright 2020, OpenTelemetry Authors
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
	"errors"
	"fmt"
	"sync"

	"github.com/elastic/go-lumber/lj"
	"github.com/elastic/go-lumber/server"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.uber.org/zap"
)

var (
	errEmptyEndpoint       = errors.New("empty endpoint")
	errNilNextLogsConsumer = errors.New("nil logsConsumer")
)

// FilebeatReceiver implements on the Lumberjack protocol.
type FilebeatReceiver struct {
	sync.Mutex
	logger       *zap.Logger
	config       *Config
	logsConsumer consumer.Logs
	server       server.Server
}

// NewLogsReceiver creates the Filebeat receiver with the given configuration.
func NewLogsReceiver(
	logger *zap.Logger,
	config Config,
	nextConsumer consumer.Logs,
) (component.LogsReceiver, error) {
	if nextConsumer == nil {
		return nil, errNilNextLogsConsumer
	}

	if config.Endpoint == "" {
		return nil, errEmptyEndpoint
	}

	r := &FilebeatReceiver{
		logger:       logger,
		config:       &config,
		logsConsumer: nextConsumer,
	}

	return r, nil
}

// Start tells the receiver to start its processing.
// By convention the consumer of the received data is set when the receiver
// instance is created.
func (r *FilebeatReceiver) Start(ctx context.Context, host component.Host) error {
	r.Lock()
	defer r.Unlock()

	ljServer, err := server.ListenAndServe(r.config.Endpoint,
		server.V1(false),
		server.V2(true))
	if err != nil {
		return fmt.Errorf("failed to configure tcp listener: %w", err)
	}
	r.server = ljServer

	r.goReceive(ctx)
	return nil
}

// goReceive will receive events of lumberjack connections.
func (r *FilebeatReceiver) goReceive(ctx context.Context) {
	go func() {
		for batch := range r.server.ReceiveChan() {
			err := r.consumeLogs(ctx, batch)
			if err != nil {
				return
			}
		}
	}()
}

// Shutdown tells the receiver that should stop reception,
// giving it a chance to perform any necessary clean-up.
func (r *FilebeatReceiver) Shutdown(context.Context) error {
	r.Lock()
	defer r.Unlock()

	err := r.server.Close()

	return err
}

func (r *FilebeatReceiver) consumeLogs(ctx context.Context, batch *lj.Batch) error {
	ld, err := FilebeatToLogData(r.logger, batch)
	if err != nil {
		return fmt.Errorf("failed to ship logs to filebeat: %w", err)
	}

	decodeErr := r.logsConsumer.ConsumeLogs(ctx, ld)

	if decodeErr != nil {
		return fmt.Errorf("failed to consume logs: %w", decodeErr)
	}
	batch.ACK()
	return nil
}

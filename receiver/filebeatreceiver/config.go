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

package filebeatreceiver // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/filebeatreceiver"

import (
	"go.opentelemetry.io/collector/config/confignet"
	"go.opentelemetry.io/collector/config/configtls"
)

// Config defines configuration for the Filebeat receiver.
type Config struct {
	confignet.TCPAddr `mapstructure:",squash"` // squash ensures fields are correctly decoded in embedded struct
	// Endpoint configures the listening address for the server.
	Endpoint string `mapstructure:"endpoint"`

	// TLSSetting struct exposes TLS client configuration.
	TLSSetting *configtls.TLSServerSetting `mapstructure:"tls, omitempty"`
}

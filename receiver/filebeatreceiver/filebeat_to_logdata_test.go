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
	"testing"

	"github.com/elastic/go-lumber/lj"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.uber.org/zap"
)

func Test_FilebeatToLogData(t *testing.T) {
	var events []interface{}
	events = append(events, makeEvent())
	result, err := FilebeatToLogData(nil, lj.NewBatch(events))
	assert.NoError(t, err)
	assert.NotEmpty(t, result)

	logSlice := createLogsSlice()
	assert.Equal(t, logSlice.At(0), result.ResourceLogs().At(0))
}

func makeEvent() interface{} {
	return map[string]interface{}{
		"@timestamp": "2022-12-21T10:03:35.178Z",
		"type":       "filebeat",
		"message":    "shrenik",
		"@metadata": map[string]interface{}{
			"beat":    "filebeat",
			"version": "1",
		},
	}
}

func createLogsSlice() plog.ResourceLogsSlice {
	lrs := plog.NewResourceLogsSlice()
	lr := lrs.AppendEmpty()
	sl := lr.ScopeLogs().AppendEmpty()
	logRecord := sl.LogRecords().AppendEmpty()
	logRecord.Body().SetStr("shrenik")
	logRecord.SetTimestamp(pcommon.Timestamp(1671617015178000000))
	logRecord.Attributes().PutStr("@timestamp", "2022-12-21T10:03:35.178Z")
	logRecord.Attributes().PutStr("type", "filebeat")
	metadata := map[string]interface{}{
		"beat":    "filebeat",
		"version": "1",
	}
	_ = addAttributeValue(nil, metadata, logRecord.Attributes().PutEmpty("@metadata"))
	sl.Scope().SetName("filebeat")
	sl.Scope().SetVersion("1")
	return lrs
}

func TestAddAttributeValueEmpty(t *testing.T) {
	value := pcommon.NewValueEmpty()
	assert.NoError(t, addAttributeValue(zap.NewNop(), nil, value))
	assert.Equal(t, pcommon.NewValueEmpty(), value)
}

func TestAddAttributeValueString(t *testing.T) {
	value := pcommon.NewValueEmpty()
	assert.NoError(t, addAttributeValue(zap.NewNop(), "foo", value))
	assert.Equal(t, pcommon.NewValueStr("foo"), value)
}

func TestAddAttributeValueBool(t *testing.T) {
	value := pcommon.NewValueEmpty()
	assert.NoError(t, addAttributeValue(zap.NewNop(), false, value))
	assert.Equal(t, pcommon.NewValueBool(false), value)
}

func TestAddAttributeValueFloat(t *testing.T) {
	value := pcommon.NewValueEmpty()
	assert.NoError(t, addAttributeValue(zap.NewNop(), 12.3, value))
	assert.Equal(t, pcommon.NewValueDouble(12.3), value)
}

func TestAddAttributeValueMap(t *testing.T) {
	value := pcommon.NewValueEmpty()
	assert.NoError(t, addAttributeValue(zap.NewNop(), map[string]interface{}{"foo": "bar"}, value))
	atts := pcommon.NewValueMap()
	attMap := atts.Map()
	attMap.PutStr("foo", "bar")
	assert.Equal(t, atts, value)
}

func TestAddAttributeValueArray(t *testing.T) {
	value := pcommon.NewValueEmpty()
	assert.NoError(t, addAttributeValue(zap.NewNop(), []interface{}{"foo"}, value))
	arrValue := pcommon.NewValueSlice()
	arr := arrValue.Slice()
	arr.AppendEmpty().SetStr("foo")
	assert.Equal(t, arrValue, value)
}

func TestAddAttributeValueInvalid(t *testing.T) {
	assert.Error(t, addAttributeValue(zap.NewNop(), lj.Batch{}, pcommon.NewValueEmpty()))
}

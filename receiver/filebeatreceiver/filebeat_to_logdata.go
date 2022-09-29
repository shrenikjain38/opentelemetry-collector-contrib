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
	"errors"
	"sort"
	"time"

	"github.com/elastic/go-lumber/lj"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.uber.org/zap"
)

const (
	cannotConvertValue = "cannot convert field value to attribute"
)

// FilebeatToLogData transforms Filebeat events into logs
func FilebeatToLogData(logger *zap.Logger, batch *lj.Batch) (plog.Logs, error) {
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	sl := rl.ScopeLogs().AppendEmpty()
	var instrumentLibrary, instrumentLibraryVersion string
	for _, event := range batch.Events {
		logRecord := sl.LogRecords().AppendEmpty()
		attrValue, _ := event.(map[string]interface{})
		if _, ok := attrValue["@metadata"]; ok {
			instrumentLibrary = attrValue["@metadata"].(map[string]interface{})["beat"].(string)
			instrumentLibraryVersion = attrValue["@metadata"].(map[string]interface{})["version"].(string)
		}
		layout := "2006-01-02T15:04:05.000Z"
		recordTimestamp, timeErr := time.Parse(layout, attrValue["@timestamp"].(string))
		if timeErr != nil {
			logger.Debug("Unsupported time conversion", zap.Any("value", event))
			return ld, errors.New("unsupported time conversion")
		}
		logRecord.SetTimestamp(pcommon.NewTimestampFromTime(recordTimestamp))

		for k, v := range attrValue {
			if k != "message" {
				err := addAttributeValue(logger, v, logRecord.Attributes().PutEmpty(k))
				if err != nil {
					return ld, err
				}
			} else {
				logRecord.Body().SetStr(v.(string))
			}
		}

	}
	sl.Scope().SetName(instrumentLibrary)
	sl.Scope().SetVersion(instrumentLibraryVersion)

	return ld, nil

}

func addAttributeValue(logger *zap.Logger, originalValue interface{}, dest pcommon.Value) error {

	switch value := originalValue.(type) {
	case nil:
	case string:
		dest.SetStr(value)
	case int64:
		dest.SetInt(value)
	case float64:
		dest.SetDouble(value)
	case bool:
		dest.SetBool(value)
	case map[string]interface{}:
		return addAttributeMap(logger, value, dest)
	case []interface{}:
		return addSliceVal(logger, value, dest)
	default:
		logger.Debug("Unsupported value conversion", zap.Any("value", originalValue))
		return errors.New(cannotConvertValue)
	}
	return nil
}

func addSliceVal(logger *zap.Logger, originalValue []interface{}, dest pcommon.Value) error {
	arr := dest.SetEmptySlice()
	for _, elt := range originalValue {
		err := addAttributeValue(logger, elt, arr.AppendEmpty())
		if err != nil {
			return err
		}
	}
	return nil
}

func addAttributeMap(logger *zap.Logger, originalValue map[string]interface{}, dest pcommon.Value) error {
	attrMap := dest.SetEmptyMap()
	keys := make([]string, 0, len(originalValue))
	for k := range originalValue {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := originalValue[k]
		if err := addAttributeValue(logger, v, attrMap.PutEmpty(k)); err != nil {
			return err
		}
	}
	return nil
}

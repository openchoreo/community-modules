// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package perfmetrics

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/monitor/query/azlogs"
)

func mapResourceRows(resp azlogs.QueryWorkspaceResponse) (*ResourceMetricsResult, error) {
	res := &ResourceMetricsResult{}
	if len(resp.Tables) == 0 {
		return res, nil
	}
	t := resp.Tables[0]
	idx, err := buildColumnIndex(t.Columns)
	if err != nil {
		return nil, err
	}
	for _, required := range []string{"CounterName", "TimeGenerated", "Value"} {
		if _, ok := idx[required]; !ok {
			return nil, fmt.Errorf("perfmetrics: missing required column %q", required)
		}
	}

	for _, row := range t.Rows {
		counter := rowString(row, idx, "CounterName")
		ts := rowTime(row, idx, "TimeGenerated")
		val := rowFloat(row, idx, "Value")

		switch counter {
		case CounterCPUUsageNanoCores:
			res.CPUUsage = append(res.CPUUsage, point(ts, val/nanoCoresPerCore))
		case CounterCPURequestNanoCores:
			res.CPURequests = append(res.CPURequests, point(ts, val/nanoCoresPerCore))
		case CounterCPULimitNanoCores:
			res.CPULimits = append(res.CPULimits, point(ts, val/nanoCoresPerCore))
		case CounterMemoryWorkingSetBytes:
			res.MemoryUsage = append(res.MemoryUsage, point(ts, val))
		case CounterMemoryRequestBytes:
			res.MemoryRequests = append(res.MemoryRequests, point(ts, val))
		case CounterMemoryLimitBytes:
			res.MemoryLimits = append(res.MemoryLimits, point(ts, val))
		}
	}
	return res, nil
}

func point(ts time.Time, v float64) TimeValuePoint {
	return TimeValuePoint{Timestamp: ts, Value: v}
}

func buildColumnIndex(cols []azlogs.Column) (map[string]int, error) {
	if len(cols) == 0 {
		return nil, errors.New("perfmetrics: response had no columns")
	}
	idx := make(map[string]int, len(cols))
	for i, c := range cols {
		if c.Name == nil {
			return nil, fmt.Errorf("perfmetrics: column %d has no name", i)
		}
		idx[*c.Name] = i
	}
	return idx, nil
}

func rowString(row azlogs.Row, idx map[string]int, name string) string {
	i, ok := idx[name]
	if !ok || i >= len(row) {
		return ""
	}
	switch v := row[i].(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", v)
	}
}

func rowFloat(row azlogs.Row, idx map[string]int, name string) float64 {
	i, ok := idx[name]
	if !ok || i >= len(row) {
		return 0
	}
	switch v := row[i].(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int64:
		return float64(v)
	case int:
		return float64(v)
	case string:
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return 0
		}
		return f
	default:
		return 0
	}
}

func rowTime(row azlogs.Row, idx map[string]int, name string) time.Time {
	s := rowString(row, idx, name)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

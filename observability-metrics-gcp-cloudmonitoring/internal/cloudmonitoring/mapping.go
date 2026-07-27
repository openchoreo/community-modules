// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudmonitoring

import (
	"sort"
	"time"

	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
)

// timeSeriesToPoints flattens the ListTimeSeries result into an ascending
// series. REDUCE_SUM normally collapses everything into one time series, but
// if the API still returns several (e.g. a metric-label split we did not
// anticipate), their values are summed per aligned timestamp rather than
// silently dropped.
func timeSeriesToPoints(series []*monitoringpb.TimeSeries) []TimeValuePoint {
	sums := map[time.Time]float64{}
	for _, ts := range series {
		for _, p := range ts.GetPoints() {
			v, ok := pointValue(p.GetValue())
			if !ok {
				continue
			}
			end := p.GetInterval().GetEndTime()
			if end == nil {
				continue
			}
			sums[end.AsTime().UTC()] += v
		}
	}

	points := make([]TimeValuePoint, 0, len(sums))
	for t, v := range sums {
		points = append(points, TimeValuePoint{Timestamp: t, Value: v})
	}
	sort.Slice(points, func(i, j int) bool { return points[i].Timestamp.Before(points[j].Timestamp) })
	return points
}

// pointValue extracts a numeric sample. Aggregated series are DOUBLE, but raw
// gauge metrics such as memory bytes are INT64, so both are accepted.
func pointValue(v *monitoringpb.TypedValue) (float64, bool) {
	switch tv := v.GetValue().(type) {
	case *monitoringpb.TypedValue_DoubleValue:
		return tv.DoubleValue, true
	case *monitoringpb.TypedValue_Int64Value:
		return float64(tv.Int64Value), true
	default:
		return 0, false
	}
}

// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudwatchmetrics

import (
	"math"
	"sort"
	"strconv"
	"strings"
)

// leBucket is one classic-histogram bucket: the cumulative count of observations
// whose value is <= UpperBound. Buckets are keyed by their Prometheus `le` label.
type leBucket struct {
	upperBound float64
	cumulative float64
}

// parseLE parses a Prometheus `le` bucket bound. "+Inf" (any case) maps to +Inf.
func parseLE(value string) (float64, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if strings.EqualFold(value, "+Inf") || strings.EqualFold(value, "Inf") {
		return math.Inf(1), true
	}
	f, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(f) {
		return 0, false
	}
	return f, true
}

// histogramQuantile reconstructs the phi-quantile from classic Prometheus
// histogram buckets, matching PromQL histogram_quantile() so the CloudWatch
// adapter yields the same percentiles as the Prometheus reference module.
//
// Buckets are cumulative counts (count of observations <= upperBound) and must
// include the +Inf bucket, which carries the total. Durations are non-negative,
// so the implicit lower bound of the smallest bucket is 0. Returns (value, true)
// when a quantile can be computed and (0, false) when there is no usable data.
func histogramQuantile(phi float64, buckets []leBucket) (float64, bool) {
	if len(buckets) == 0 {
		return 0, false
	}
	sort.Slice(buckets, func(i, j int) bool { return buckets[i].upperBound < buckets[j].upperBound })

	// The largest bucket is the total. Prometheus requires it to be +Inf; if it
	// is missing we still use the largest cumulative count as the total.
	total := buckets[len(buckets)-1].cumulative
	if total <= 0 {
		return 0, false
	}
	if phi < 0 {
		return 0, true
	}
	if phi > 1 {
		return math.Inf(1), true
	}

	rank := phi * total
	// Search among the finite buckets only (exclude the trailing +Inf bucket
	// when present, so interpolation always has a real upper bound).
	n := len(buckets)
	if math.IsInf(buckets[n-1].upperBound, 1) {
		n--
	}
	if n == 0 {
		// Only the +Inf bucket exists: no finite bound to report.
		return 0, false
	}
	b := sort.Search(n, func(i int) bool { return buckets[i].cumulative >= rank })
	if b == n {
		// Rank falls beyond the largest finite bucket: return its upper bound.
		return buckets[n-1].upperBound, true
	}

	bucketEnd := buckets[b].upperBound
	bucketStart := 0.0
	count := buckets[b].cumulative
	rankInBucket := rank
	if b > 0 {
		bucketStart = buckets[b-1].upperBound
		count -= buckets[b-1].cumulative
		rankInBucket -= buckets[b-1].cumulative
	}
	if count <= 0 {
		return bucketEnd, true
	}
	return bucketStart + (bucketEnd-bucketStart)*(rankInBucket/count), true
}

// bucketsFromLECounts builds a sorted cumulative-bucket slice from a map of
// `le` bound to summed count. Invalid `le` values are skipped.
func bucketsFromLECounts(leCounts map[string]float64) []leBucket {
	buckets := make([]leBucket, 0, len(leCounts))
	for le, count := range leCounts {
		bound, ok := parseLE(le)
		if !ok {
			continue
		}
		buckets = append(buckets, leBucket{upperBound: bound, cumulative: count})
	}
	return buckets
}

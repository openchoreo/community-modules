// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package loganalytics

import (
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/monitor/query/azlogs"
)

// mapEventRows maps the events statement of an events response.
//
// Only a row without a timestamp is dropped: every other field is optional on
// EventEntry. Dropping more would leave the page shorter than Total accounts
// for, which a caller reads as a truncated window.
func mapEventRows(t azlogs.Table) ([]EventEntry, []error, error) {
	idx, err := buildColumnIndex(t.Columns)
	if err != nil {
		return nil, nil, err
	}

	out := make([]EventEntry, 0, len(t.Rows))
	var skipped []error
	for i, row := range t.Rows {
		ts := rowTime(row, idx, "TimeGenerated")
		if ts.IsZero() {
			skipped = append(skipped, fmt.Errorf("%w: row %d has no parseable TimeGenerated", ErrSkipRow, i))
			continue
		}
		out = append(out, EventEntry{
			Timestamp:       ts,
			Message:         rowString(row, idx, "Message"),
			Type:            rowString(row, idx, "EventType"),
			Reason:          rowString(row, idx, "Reason"),
			ObjectNamespace: rowString(row, idx, "ObjectNamespace"),
			ObjectKind:      rowString(row, idx, "ObjectKind"),
			ObjectName:      rowString(row, idx, "ObjectName"),
			NamespaceName:   rowString(row, idx, "NamespaceName"),
			ComponentName:   rowString(row, idx, "ComponentName"),
			ComponentUID:    rowString(row, idx, "ComponentUID"),
			ProjectName:     rowString(row, idx, "ProjectName"),
			ProjectUID:      rowString(row, idx, "ProjectUID"),
			EnvironmentName: rowString(row, idx, "EnvironmentName"),
			EnvironmentUID:  rowString(row, idx, "EnvironmentUID"),
		})
	}
	return out, skipped, nil
}

// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudlogging

import (
	"strings"
	"testing"
)

func TestBuildComponentLogsFilter_ReleaseLine(t *testing.T) {
	const lineUID = "93f65294-926f-475b-98f1-dc4f152f38da"
	want := `labels."k8s-pod/openchoreo_dev/release-line-uid"="` + lineUID + `"`

	f := BuildComponentLogsFilter(ComponentLogsParams{
		Namespace:      "default",
		ComponentUID:   "846bb581-d6ba-446d-a160-8cec468d2219",
		ReleaseLineUID: lineUID,
	})
	if !strings.Contains(f, want) {
		t.Errorf("filter missing release line clause %q:\n%s", want, f)
	}
	if !strings.Contains(f, `labels."k8s-pod/openchoreo_dev/component-uid"=`) {
		t.Errorf("filter lost the component clause:\n%s", f)
	}

	f = BuildComponentLogsFilter(ComponentLogsParams{Namespace: "default", ComponentUID: "846bb581-d6ba-446d-a160-8cec468d2219"})
	if strings.Contains(f, "release-line-uid") {
		t.Errorf("filter without a release line must not filter on it:\n%s", f)
	}
}

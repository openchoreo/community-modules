// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudmonitoring

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakeAlertAPI is an in-memory alertPolicyAPI keyed by resource name.
type fakeAlertAPI struct {
	policies   map[string]*monitoringpb.AlertPolicy
	createErr  error
	listErr    error
	deleteErr  error
	nextIDSeq  int
	created    []*monitoringpb.AlertPolicy
	deletedIDs []string
}

func newFakeAlertAPI() *fakeAlertAPI {
	return &fakeAlertAPI{policies: map[string]*monitoringpb.AlertPolicy{}}
}

func (f *fakeAlertAPI) CreateAlertPolicy(_ context.Context, req *monitoringpb.CreateAlertPolicyRequest) (*monitoringpb.AlertPolicy, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	f.nextIDSeq++
	p := req.GetAlertPolicy()
	// The API assigns the resource name on create.
	name := req.GetName() + "/alertPolicies/" + string(rune('a'+f.nextIDSeq))
	p.Name = name
	f.policies[name] = p
	f.created = append(f.created, p)
	return p, nil
}

func (f *fakeAlertAPI) GetAlertPolicy(_ context.Context, req *monitoringpb.GetAlertPolicyRequest) (*monitoringpb.AlertPolicy, error) {
	if p, ok := f.policies[req.GetName()]; ok {
		return p, nil
	}
	return nil, status.Error(codes.NotFound, "not found")
}

func (f *fakeAlertAPI) DeleteAlertPolicy(_ context.Context, req *monitoringpb.DeleteAlertPolicyRequest) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	if _, ok := f.policies[req.GetName()]; !ok {
		return status.Error(codes.NotFound, "not found")
	}
	delete(f.policies, req.GetName())
	f.deletedIDs = append(f.deletedIDs, req.GetName())
	return nil
}

func (f *fakeAlertAPI) UpdateAlertPolicy(_ context.Context, req *monitoringpb.UpdateAlertPolicyRequest) (*monitoringpb.AlertPolicy, error) {
	p := req.GetAlertPolicy()
	f.policies[p.GetName()] = p
	return p, nil
}

func (f *fakeAlertAPI) ListAlertPolicies(_ context.Context, req *monitoringpb.ListAlertPoliciesRequest) ([]*monitoringpb.AlertPolicy, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	// The fake honours the hash- and rule-name-label sub-filters (the pieces
	// findByHash / FindRuleByName rely on); managedFilter() is assumed to
	// match all our policies since every one is created via the translator.
	var out []*monitoringpb.AlertPolicy
	for _, p := range f.policies {
		out = append(out, p)
	}
	for _, label := range []string{policyLabelRuleHash, policyLabelRuleName} {
		if want := extractLabelFromFilter(req.GetFilter(), label); want != "" {
			var filtered []*monitoringpb.AlertPolicy
			for _, p := range out {
				if p.GetUserLabels()[label] == want {
					filtered = append(filtered, p)
				}
			}
			return filtered, nil
		}
	}
	return out, nil
}

// extractLabelFromFilter pulls a user-label value out of a filter string of
// the form ... user_labels."<label>"="<value>".
func extractLabelFromFilter(filter, label string) string {
	marker := label + `"="`
	i := strings.Index(filter, marker)
	if i < 0 {
		return ""
	}
	rest := filter[i+len(marker):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		return rest
	}
	return rest[:j]
}

func newTestAlertClient(api alertPolicyAPI) *AlertClient {
	return &AlertClient{
		projectID: "my-project",
		cfg:       baseTranslatorCfg(),
		timeout:   5 * time.Second,
		api:       api,
		logger:    slog.New(slog.DiscardHandler),
	}
}

func TestCreateRuleThenConflict(t *testing.T) {
	api := newFakeAlertAPI()
	c := newTestAlertClient(api)

	res, err := c.CreateRule(context.Background(), validRuleInput())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if res.BackendID == "" {
		t.Errorf("backend ID empty")
	}
	if res.LogicalID != "high-cpu" {
		t.Errorf("logical ID = %q", res.LogicalID)
	}
	if len(api.created) != 1 {
		t.Errorf("expected 1 create, got %d", len(api.created))
	}

	// Second create for the same identity must conflict.
	_, err = c.CreateRule(context.Background(), validRuleInput())
	if !errors.Is(err, ErrRuleAlreadyExists) {
		t.Errorf("second create err = %v, want ErrRuleAlreadyExists", err)
	}
}

func TestFindRuleByName(t *testing.T) {
	api := newFakeAlertAPI()
	c := newTestAlertClient(api)
	if _, err := c.CreateRule(context.Background(), validRuleInput()); err != nil {
		t.Fatalf("create: %v", err)
	}

	res, ns, err := c.FindRuleByName(context.Background(), "high-cpu")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if ns != "default" {
		t.Errorf("namespace = %q", ns)
	}
	if res.LogicalID != "high-cpu" {
		t.Errorf("logical ID = %q", res.LogicalID)
	}

	if _, _, err := c.FindRuleByName(context.Background(), "nope"); !errors.Is(err, ErrRuleNotFound) {
		t.Errorf("find missing err = %v, want ErrRuleNotFound", err)
	}
}

func TestUpdateRule(t *testing.T) {
	api := newFakeAlertAPI()
	c := newTestAlertClient(api)

	// Update with no existing rule is rejected, never creates.
	if _, err := c.UpdateRule(context.Background(), validRuleInput()); !errors.Is(err, ErrRuleNotFound) {
		t.Fatalf("update(missing) err = %v, want ErrRuleNotFound", err)
	}
	if len(api.created) != 0 {
		t.Fatalf("update of a missing rule must not create, got %d creates", len(api.created))
	}

	res, err := c.CreateRule(context.Background(), validRuleInput())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	firstName := res.BackendID

	// Update replaces in place (same resource name, no new create).
	in := validRuleInput()
	in.Threshold = 0.95
	res2, err := c.UpdateRule(context.Background(), in)
	if err != nil {
		t.Fatalf("update(replace): %v", err)
	}
	if res2.BackendID != firstName {
		t.Errorf("update changed resource name: %q → %q", firstName, res2.BackendID)
	}
	if len(api.created) != 1 {
		t.Errorf("update should not create a second policy, got %d", len(api.created))
	}
}

func TestDeleteRule(t *testing.T) {
	api := newFakeAlertAPI()
	c := newTestAlertClient(api)
	if _, err := c.CreateRule(context.Background(), validRuleInput()); err != nil {
		t.Fatalf("create: %v", err)
	}

	res, err := c.DeleteRule(context.Background(), "high-cpu")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(api.deletedIDs) != 1 || api.deletedIDs[0] != res.BackendID {
		t.Errorf("delete IDs = %v", api.deletedIDs)
	}

	if _, err := c.DeleteRule(context.Background(), "high-cpu"); !errors.Is(err, ErrRuleNotFound) {
		t.Errorf("delete again err = %v, want ErrRuleNotFound", err)
	}
}

func TestCreateRulePropagatesValidationError(t *testing.T) {
	api := newFakeAlertAPI()
	c := newTestAlertClient(api)
	in := validRuleInput()
	in.Operator = "bogus"
	_, err := c.CreateRule(context.Background(), in)
	if !errors.Is(err, ErrValidation) {
		t.Errorf("err = %v, want ErrValidation", err)
	}
	if len(api.created) != 0 {
		t.Errorf("no policy should be created on validation failure")
	}
}

// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package k8seventenrichprocessor // import "github.com/openchoreo/community-modules/observability-events-otel-collector/k8seventenrichprocessor"

import (
	"go.opentelemetry.io/collector/pdata/pcommon"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// fieldExtractor copies a metadata map onto resource attributes, honouring allow-list / deny-list and a key prefix.
type fieldExtractor struct {
	enabled bool
	prefix  string
	include map[string]struct{}
	exclude map[string]struct{}
}

func newFieldExtractor(c FieldConfig) fieldExtractor {
	return fieldExtractor{
		enabled: c.Enabled,
		prefix:  c.Prefix,
		include: toSet(c.Include),
		exclude: toSet(c.Exclude),
	}
}

func (f fieldExtractor) allow(key string) bool {
	if _, excluded := f.exclude[key]; excluded {
		return false
	}
	if len(f.include) == 0 {
		return true
	}
	_, included := f.include[key]
	return included
}

func (f fieldExtractor) inject(attrs pcommon.Map, m map[string]string) {
	if !f.enabled {
		return
	}
	for k, v := range m {
		if !f.allow(k) {
			continue
		}
		attrs.PutStr(f.prefix+k, v)
	}
}

// ownerExtractor writes the controlling owner reference (kind/name/uid) onto resource attributes.
type ownerExtractor struct {
	enabled bool
	prefix  string
}

func newOwnerExtractor(c OwnerConfig) ownerExtractor {
	return ownerExtractor{enabled: c.Enabled, prefix: c.Prefix}
}

func (o ownerExtractor) inject(attrs pcommon.Map, refs []metav1.OwnerReference) {
	if !o.enabled {
		return
	}
	ref := controllerOwner(refs)
	if ref == nil {
		return
	}
	attrs.PutStr(o.prefix+"kind", ref.Kind)
	attrs.PutStr(o.prefix+"name", ref.Name)
	attrs.PutStr(o.prefix+"uid", string(ref.UID))
}

// controllerOwner returns the ownerReference with controller=true, or nil if none.
func controllerOwner(refs []metav1.OwnerReference) *metav1.OwnerReference {
	for i := range refs {
		if refs[i].Controller != nil && *refs[i].Controller {
			return &refs[i]
		}
	}
	return nil
}

func toSet(keys []string) map[string]struct{} {
	if len(keys) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		set[k] = struct{}{}
	}
	return set
}

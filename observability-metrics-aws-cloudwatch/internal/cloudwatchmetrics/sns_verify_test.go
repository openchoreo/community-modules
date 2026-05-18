// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudwatchmetrics

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1" //nolint:gosec // Test covers SNS SignatureVersion=1 behaviour.
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestVerifySNSMessageSignatureNotificationSHA1(t *testing.T) {
	priv, cert := generateRSACert(t)

	prev := fetchSigningCertFunc
	fetchSigningCertFunc = func(string) (*x509.Certificate, error) { return cert, nil }
	t.Cleanup(func() { fetchSigningCertFunc = prev })

	env := &SNSEnvelopeResult{
		EnvelopeType:     "Notification",
		MessageID:        "msg-1",
		TopicARN:         "arn:aws:sns:eu-north-1:123456789012:alerts",
		RawMessage:       `{"AlarmName":"oc-metrics-alert-x","NewStateValue":"ALARM"}`,
		Subject:          "ALARM: \"test\" in EU (Stockholm)",
		Timestamp:        "2026-04-23T10:00:00Z",
		SignatureVersion: "1",
		SigningCertURL:   "https://sns.eu-north-1.amazonaws.com/SimpleNotificationService.pem",
	}
	msg, err := buildCanonicalMessageToSign(env)
	if err != nil {
		t.Fatalf("buildCanonicalMessageToSign() error = %v", err)
	}
	sum := sha1.Sum([]byte(msg)) //nolint:gosec // dictated by SNS SignatureVersion=1.
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA1, sum[:])
	if err != nil {
		t.Fatalf("SignPKCS1v15: %v", err)
	}
	env.Signature = base64.StdEncoding.EncodeToString(sig)

	if err := VerifySNSMessageSignature(env); err != nil {
		t.Fatalf("VerifySNSMessageSignature() error = %v", err)
	}
}

func TestVerifySNSMessageSignatureNotificationSHA256(t *testing.T) {
	priv, cert := generateRSACert(t)

	prev := fetchSigningCertFunc
	fetchSigningCertFunc = func(string) (*x509.Certificate, error) { return cert, nil }
	t.Cleanup(func() { fetchSigningCertFunc = prev })

	env := &SNSEnvelopeResult{
		EnvelopeType:     "Notification",
		MessageID:        "msg-2",
		TopicARN:         "arn:aws:sns:eu-north-1:123456789012:alerts",
		RawMessage:       `{"AlarmName":"oc-metrics-alert-y","NewStateValue":"OK"}`,
		Timestamp:        "2026-04-23T11:00:00Z",
		SignatureVersion: "2",
		SigningCertURL:   "https://sns.eu-north-1.amazonaws.com/SimpleNotificationService.pem",
	}
	msg, _ := buildCanonicalMessageToSign(env)
	sum := sha256.Sum256([]byte(msg))
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatalf("SignPKCS1v15: %v", err)
	}
	env.Signature = base64.StdEncoding.EncodeToString(sig)

	if err := VerifySNSMessageSignature(env); err != nil {
		t.Fatalf("VerifySNSMessageSignature() error = %v", err)
	}
}

func TestValidateSigningCertURLAccepts(t *testing.T) {
	if err := validateSigningCertURL("https://sns.eu-north-1.amazonaws.com/cert.pem"); err != nil {
		t.Fatalf("expected valid SNS URL to pass: %v", err)
	}
	if err := validateSigningCertURL("https://sns.cn-north-1.amazonaws.com.cn/cert.pem"); err != nil {
		t.Fatalf("expected valid CN SNS URL to pass: %v", err)
	}
}

func TestValidateSigningCertURLRejects(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{"http scheme", "http://sns.eu-north-1.amazonaws.com/cert.pem"},
		{"non-sns host", "https://attacker.example.com/cert.pem"},
		{"non-amazonaws", "https://sns.example.com/cert.pem"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateSigningCertURL(tc.raw); err == nil {
				t.Fatalf("expected error for %q", tc.raw)
			}
		})
	}
}

func TestVerifySNSMessageSignatureRejectsNilEnvelope(t *testing.T) {
	if err := VerifySNSMessageSignature(nil); err == nil {
		t.Fatal("expected nil envelope to be rejected")
	}
}

func TestVerifySNSMessageSignatureRequiresSignatureFields(t *testing.T) {
	if err := VerifySNSMessageSignature(&SNSEnvelopeResult{}); err == nil {
		t.Fatal("expected missing fields to be rejected")
	}
}

func TestVerifySNSMessageSignatureRejectsBadCertURL(t *testing.T) {
	env := &SNSEnvelopeResult{
		Signature:      "AAAA",
		SigningCertURL: "http://attacker.example.com/cert.pem",
	}
	if err := VerifySNSMessageSignature(env); err == nil {
		t.Fatal("expected bad cert URL to be rejected")
	}
}

func TestVerifySNSMessageSignaturePropagatesFetchError(t *testing.T) {
	prev := fetchSigningCertFunc
	t.Cleanup(func() { fetchSigningCertFunc = prev })
	fetchSigningCertFunc = func(string) (*x509.Certificate, error) {
		return nil, errors.New("network down")
	}
	env := &SNSEnvelopeResult{
		Signature:      "AAAA",
		SigningCertURL: "https://sns.eu-north-1.amazonaws.com/cert.pem",
	}
	if err := VerifySNSMessageSignature(env); err == nil {
		t.Fatal("expected fetch failure to propagate")
	}
}

func TestVerifySNSMessageSignatureRejectsNonRSACert(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}
	prev := fetchSigningCertFunc
	t.Cleanup(func() { fetchSigningCertFunc = prev })
	fetchSigningCertFunc = func(string) (*x509.Certificate, error) { return cert, nil }

	env := &SNSEnvelopeResult{
		Signature:        "AAAA",
		SigningCertURL:   "https://sns.eu-north-1.amazonaws.com/cert.pem",
		EnvelopeType:     "Notification",
		Timestamp:        "2026-04-23T10:00:00Z",
		SignatureVersion: "1",
	}
	if err := VerifySNSMessageSignature(env); err == nil || !strings.Contains(err.Error(), "RSA") {
		t.Fatalf("expected non-RSA cert to be rejected, got %v", err)
	}
}

func TestVerifySNSMessageSignatureRejectsBadBase64(t *testing.T) {
	prev := fetchSigningCertFunc
	t.Cleanup(func() { fetchSigningCertFunc = prev })
	_, cert := generateRSACert(t)
	fetchSigningCertFunc = func(string) (*x509.Certificate, error) { return cert, nil }

	env := &SNSEnvelopeResult{
		Signature:        "***not-base64***",
		SigningCertURL:   "https://sns.eu-north-1.amazonaws.com/cert.pem",
		EnvelopeType:     "Notification",
		Timestamp:        "2026-04-23T10:00:00Z",
		SignatureVersion: "1",
	}
	if err := VerifySNSMessageSignature(env); err == nil {
		t.Fatal("expected base64 decode error")
	}
}

func TestVerifySNSMessageSignatureRejectsUnknownVersion(t *testing.T) {
	prev := fetchSigningCertFunc
	t.Cleanup(func() { fetchSigningCertFunc = prev })
	_, cert := generateRSACert(t)
	fetchSigningCertFunc = func(string) (*x509.Certificate, error) { return cert, nil }

	env := &SNSEnvelopeResult{
		Signature:        "QUFB",
		SigningCertURL:   "https://sns.eu-north-1.amazonaws.com/cert.pem",
		EnvelopeType:     "Notification",
		Timestamp:        "2026-04-23T10:00:00Z",
		SignatureVersion: "9",
	}
	if err := VerifySNSMessageSignature(env); err == nil {
		t.Fatal("expected unknown signature version to error")
	}
}

func TestBuildCanonicalMessageToSignSubscriptionRequiresFields(t *testing.T) {
	if _, err := buildCanonicalMessageToSign(&SNSEnvelopeResult{
		EnvelopeType: "SubscriptionConfirmation",
		Timestamp:    "2026-04-23T10:00:00Z",
	}); err == nil {
		t.Fatal("expected missing token/url to error")
	}
}

func TestBuildCanonicalMessageToSignSubscriptionHappyPath(t *testing.T) {
	got, err := buildCanonicalMessageToSign(&SNSEnvelopeResult{
		EnvelopeType: "SubscriptionConfirmation",
		MessageID:    "msg-1",
		TopicARN:     "arn:aws:sns:eu-north-1:123456789012:alerts",
		Timestamp:    "2026-04-23T10:00:00Z",
		Token:        "token-1",
		SubscribeURL: "https://sns.eu-north-1.amazonaws.com/?Action=ConfirmSubscription",
		RawMessage:   "Please confirm",
	})
	if err != nil {
		t.Fatalf("buildCanonicalMessageToSign() error = %v", err)
	}
	if !strings.Contains(got, "SubscribeURL") || !strings.Contains(got, "Token") {
		t.Fatalf("expected canonical SubscriptionConfirmation message, got %q", got)
	}
}

func TestBuildCanonicalMessageToSignRequiresFields(t *testing.T) {
	if _, err := buildCanonicalMessageToSign(&SNSEnvelopeResult{}); err == nil {
		t.Fatal("expected missing fields to error")
	}
}

func TestBuildCanonicalMessageToSignRejectsUnknownType(t *testing.T) {
	if _, err := buildCanonicalMessageToSign(&SNSEnvelopeResult{
		EnvelopeType: "Mystery",
		Timestamp:    "2026-04-23T10:00:00Z",
	}); err == nil {
		t.Fatal("expected unknown envelope type to error")
	}
}

func TestFetchSigningCertHTTP(t *testing.T) {
	cert, certPEM := generateRSACertPEM(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(certPEM)
	}))
	t.Cleanup(srv.Close)

	got, err := fetchSigningCert(srv.URL)
	if err != nil {
		t.Fatalf("fetchSigningCert() error = %v", err)
	}
	if !got.Equal(cert) {
		t.Fatalf("unexpected cert returned")
	}

	got2, err := fetchSigningCert(srv.URL)
	if err != nil {
		t.Fatalf("fetchSigningCert() second error = %v", err)
	}
	if got != got2 {
		t.Fatalf("expected cached cert pointer to match")
	}
}

func TestFetchSigningCertHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	if _, err := fetchSigningCert(srv.URL + "/?fresh"); err == nil {
		t.Fatal("expected non-2xx fetch to fail")
	}
}

func TestFetchSigningCertNonPEM(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not a pem block"))
	}))
	t.Cleanup(srv.Close)

	if _, err := fetchSigningCert(srv.URL + "/?nonpem"); err == nil {
		t.Fatal("expected non-pem to fail")
	}
}

// --- helpers --------------------------------------------------------------

func generateRSACert(t *testing.T) (*rsa.PrivateKey, *x509.Certificate) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}
	return priv, cert
}

func generateRSACertPEM(t *testing.T) (*x509.Certificate, []byte) {
	t.Helper()
	_, cert := generateRSACert(t)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	return cert, pemBytes
}

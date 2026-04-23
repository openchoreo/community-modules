// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudwatch

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1" //nolint:gosec // Test covers SNS SignatureVersion=1 behaviour.
	"crypto/x509"
	"encoding/base64"
	"math/big"
	"testing"
	"time"
)

func TestVerifySNSMessageSignatureNotification(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey() error = %v", err)
	}
	certDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}, &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("x509.CreateCertificate() error = %v", err)
	}
	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		t.Fatalf("x509.ParseCertificate() error = %v", err)
	}

	prevFetcher := fetchSigningCertFunc
	fetchSigningCertFunc = func(string) (*x509.Certificate, error) {
		return cert, nil
	}
	t.Cleanup(func() {
		fetchSigningCertFunc = prevFetcher
	})

	env := &SNSEnvelopeResult{
		EnvelopeType:     "Notification",
		MessageID:        "msg-1",
		TopicARN:         "arn:aws:sns:eu-north-1:123456789012:alerts",
		RawMessage:       `{"AlarmName":"oc-logs-alert-123","NewStateValue":"ALARM"}`,
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
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA1, sum[:])
	if err != nil {
		t.Fatalf("rsa.SignPKCS1v15() error = %v", err)
	}
	env.Signature = base64.StdEncoding.EncodeToString(signature)

	if err := VerifySNSMessageSignature(env); err != nil {
		t.Fatalf("VerifySNSMessageSignature() error = %v", err)
	}
}

// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudwatchmetrics

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha1" //nolint:gosec // SNS still publishes SignatureVersion=1 messages which are SHA1.
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

var certCache sync.Map
var fetchSigningCertFunc = fetchSigningCert

// VerifySNSMessageSignature validates the SNS envelope's RSA signature.
func VerifySNSMessageSignature(env *SNSEnvelopeResult) error {
	if env == nil {
		return errors.New("nil SNS envelope")
	}
	if env.Signature == "" || env.SigningCertURL == "" {
		return errors.New("SNS envelope missing Signature or SigningCertURL")
	}
	if err := validateSigningCertURL(env.SigningCertURL); err != nil {
		return err
	}

	cert, err := fetchSigningCertFunc(env.SigningCertURL)
	if err != nil {
		return err
	}
	rsaPub, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok {
		return errors.New("SNS signing cert does not carry an RSA public key")
	}

	sig, err := base64.StdEncoding.DecodeString(env.Signature)
	if err != nil {
		return fmt.Errorf("decode SNS signature: %w", err)
	}

	msg, err := buildCanonicalMessageToSign(env)
	if err != nil {
		return err
	}

	switch env.SignatureVersion {
	case "", "1":
		h := sha1.Sum([]byte(msg)) //nolint:gosec // dictated by SNS SignatureVersion=1.
		return rsa.VerifyPKCS1v15(rsaPub, crypto.SHA1, h[:], sig)
	case "2":
		h := sha256.Sum256([]byte(msg))
		return rsa.VerifyPKCS1v15(rsaPub, crypto.SHA256, h[:], sig)
	default:
		return fmt.Errorf("unsupported SNS SignatureVersion %q", env.SignatureVersion)
	}
}

func validateSigningCertURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid SigningCertURL: %w", err)
	}
	if u.Scheme != "https" {
		return errors.New("SigningCertURL must use https")
	}
	host := strings.ToLower(u.Hostname())
	if !(strings.HasPrefix(host, "sns.") && (strings.HasSuffix(host, ".amazonaws.com") || strings.HasSuffix(host, ".amazonaws.com.cn"))) {
		return fmt.Errorf("SigningCertURL host %q is not an SNS endpoint", host)
	}
	return nil
}

func fetchSigningCert(certURL string) (*x509.Certificate, error) {
	if cached, ok := certCache.Load(certURL); ok {
		return cached.(*x509.Certificate), nil
	}

	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, _ []*http.Request) error {
			if err := validateSigningCertURL(req.URL.String()); err != nil {
				return fmt.Errorf("redirected SigningCertURL %s: %w", req.URL.Redacted(), err)
			}
			return nil
		},
	}
	req, err := http.NewRequest(http.MethodGet, certURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build signing cert request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch signing cert: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("signing cert fetch returned %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read signing cert: %w", err)
	}
	block, _ := pem.Decode(body)
	if block == nil {
		return nil, errors.New("signing cert is not a PEM block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse signing cert: %w", err)
	}
	finalURL := resp.Request.URL.String()
	certCache.Store(finalURL, cert)
	if finalURL != certURL {
		certCache.Store(certURL, cert)
	}
	return cert, nil
}

func buildCanonicalMessageToSign(env *SNSEnvelopeResult) (string, error) {
	if env.Timestamp == "" || env.EnvelopeType == "" {
		return "", errors.New("missing Timestamp or Type on SNS envelope")
	}
	var b strings.Builder
	switch env.EnvelopeType {
	case "SubscriptionConfirmation", "UnsubscribeConfirmation":
		if env.Token == "" || env.SubscribeURL == "" {
			return "", errors.New("subscription envelope missing Token or SubscribeURL")
		}
		writeField(&b, "Message", env.RawMessage)
		writeField(&b, "MessageId", env.MessageID)
		writeField(&b, "SubscribeURL", env.SubscribeURL)
		writeField(&b, "Timestamp", env.Timestamp)
		writeField(&b, "Token", env.Token)
		writeField(&b, "TopicArn", env.TopicARN)
		writeField(&b, "Type", env.EnvelopeType)
	case "Notification":
		writeField(&b, "Message", env.RawMessage)
		writeField(&b, "MessageId", env.MessageID)
		if env.Subject != "" {
			writeField(&b, "Subject", env.Subject)
		}
		writeField(&b, "Timestamp", env.Timestamp)
		writeField(&b, "TopicArn", env.TopicARN)
		writeField(&b, "Type", env.EnvelopeType)
	default:
		return "", fmt.Errorf("unsupported envelope type %q", env.EnvelopeType)
	}
	return b.String(), nil
}

func writeField(b *strings.Builder, key, value string) {
	b.WriteString(key)
	b.WriteByte('\n')
	b.WriteString(value)
	b.WriteByte('\n')
}

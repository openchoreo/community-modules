package app

import "testing"

func TestLoadConfigParsesAlertingEnv(t *testing.T) {
	t.Setenv("AWS_REGION", "eu-north-1")
	t.Setenv("CLUSTER_NAME", "openchoreo-test")
	t.Setenv("ALERT_METRIC_NAMESPACE", "Custom/Logs")
	t.Setenv("ALARM_ACTION_ARNS", "arn:aws:sns:eu-north-1:123456789012:alerts")
	t.Setenv("OK_ACTION_ARNS", "arn:aws:lambda:eu-north-1:123456789012:function:ok")
	t.Setenv("INSUFFICIENT_DATA_ACTION_ARNS", "arn:aws:sns:eu-north-1:123456789012:insufficient")
	t.Setenv("OBSERVER_URL", "http://observer.internal")
	t.Setenv("SNS_ALLOW_SUBSCRIBE_CONFIRM", "true")
	t.Setenv("FORWARD_RECOVERY", "true")
	t.Setenv("WEBHOOK_AUTH_ENABLED", "true")
	t.Setenv("WEBHOOK_SHARED_SECRET", "0123456789abcdef")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if cfg.AlertMetricNamespace != "Custom/Logs" {
		t.Fatalf("unexpected metric namespace %q", cfg.AlertMetricNamespace)
	}
	if len(cfg.AlarmActionARNs) != 1 || cfg.AlarmActionARNs[0] == "" {
		t.Fatalf("unexpected alarm action ARNs %#v", cfg.AlarmActionARNs)
	}
	if !cfg.SNSAllowSubscribeConfirm || !cfg.ForwardRecovery {
		t.Fatalf("unexpected alerting config %#v", cfg)
	}
	if !cfg.WebhookAuthEnabled || cfg.WebhookSharedSecret != "0123456789abcdef" {
		t.Fatalf("unexpected webhook auth config %#v", cfg)
	}
}

func TestLoadConfigRejectsInvalidActionARN(t *testing.T) {
	t.Setenv("AWS_REGION", "eu-north-1")
	t.Setenv("CLUSTER_NAME", "openchoreo-test")
	t.Setenv("ALARM_ACTION_ARNS", "not-an-arn")

	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected invalid ARN error")
	}
}

func TestLoadConfigRejectsTooManyActionARNs(t *testing.T) {
	t.Setenv("AWS_REGION", "eu-north-1")
	t.Setenv("CLUSTER_NAME", "openchoreo-test")
	t.Setenv("ALARM_ACTION_ARNS",
		"arn:aws:sns:eu-north-1:123456789012:a,"+
			"arn:aws:sns:eu-north-1:123456789012:b,"+
			"arn:aws:sns:eu-north-1:123456789012:c,"+
			"arn:aws:sns:eu-north-1:123456789012:d,"+
			"arn:aws:sns:eu-north-1:123456789012:e,"+
			"arn:aws:sns:eu-north-1:123456789012:f",
	)

	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected too-many-ARNs error")
	}
}

func TestLoadConfigRejectsMissingWebhookSecretWhenAuthEnabled(t *testing.T) {
	t.Setenv("AWS_REGION", "eu-north-1")
	t.Setenv("CLUSTER_NAME", "openchoreo-test")
	t.Setenv("WEBHOOK_AUTH_ENABLED", "true")

	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected missing webhook secret error")
	}
}

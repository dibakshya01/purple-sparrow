package config

import "testing"

func TestDefaultsAreValid(t *testing.T) {
	if err := Defaults().Validate(); err != nil {
		t.Fatalf("defaults should be valid, got: %v", err)
	}
}

func TestLoadAppliesEnvOverrides(t *testing.T) {
	t.Setenv("PS_ADDR", ":9999")
	t.Setenv("PS_LOG_LEVEL", "debug")
	t.Setenv("PS_LOG_FORMAT", "text")
	t.Setenv("PS_TIER", "solo")

	c, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Addr != ":9999" {
		t.Errorf("Addr = %q, want :9999", c.Addr)
	}
	if c.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want debug", c.LogLevel)
	}
	if c.LogFormat != LogText {
		t.Errorf("LogFormat = %q, want text", c.LogFormat)
	}
	if c.Tier != TierSolo {
		t.Errorf("Tier = %q, want solo", c.Tier)
	}
}

func TestLoadRejectsInvalidTier(t *testing.T) {
	t.Setenv("PS_TIER", "bogus")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for invalid PS_TIER, got nil")
	}
}

func TestLoadRejectsInvalidLogLevel(t *testing.T) {
	t.Setenv("PS_LOG_LEVEL", "louder")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for invalid PS_LOG_LEVEL, got nil")
	}
}

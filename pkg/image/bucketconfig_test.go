package image

import (
	yaml2 "gopkg.in/yaml.v3"
	"testing"
)

func TestBucketConfigPoliciesRoundtrip(t *testing.T) {
	input := `
policies:
  - name: no-critical-vulns
    check: scan
    max_severity: HIGH
  - name: max-age
    check: age
    max_days: 90
  - name: require-signature
    check: signed
    key_ref: cosign.pub
  - name: max-size
    check: size
    max_bytes: 1073741824
`
	var cfg BucketConfig
	if err := yaml2.Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Policies) != 4 {
		t.Fatalf("expected 4 policies, got %d", len(cfg.Policies))
	}
	if cfg.Policies[0].Name != "no-critical-vulns" {
		t.Errorf("unexpected name: %s", cfg.Policies[0].Name)
	}
	if cfg.Policies[0].Check != PolicyCheckScan {
		t.Errorf("unexpected check: %s", cfg.Policies[0].Check)
	}
	if cfg.Policies[0].MaxSeverity != "HIGH" {
		t.Errorf("unexpected max_severity: %s", cfg.Policies[0].MaxSeverity)
	}
	if cfg.Policies[1].MaxDays != 90 {
		t.Errorf("unexpected max_days: %d", cfg.Policies[1].MaxDays)
	}
	if cfg.Policies[2].KeyRef != "cosign.pub" {
		t.Errorf("unexpected key_ref: %s", cfg.Policies[2].KeyRef)
	}
	if cfg.Policies[3].MaxBytes != 1073741824 {
		t.Errorf("unexpected max_bytes: %d", cfg.Policies[3].MaxBytes)
	}
}

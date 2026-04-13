# v1.11.0 Policy & SBOM Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `s3lo config validate` for policy-based compliance gates and `s3lo sbom` for SBOM generation from stored images.

**Architecture:** Two independent features. `config validate` extends `BucketConfig` with a `policies` block and runs checks (scan, age, signed, size) against a specific image tag. `sbom` is a thin wrapper over the same Trivy invocation used by `scan`, just with SBOM output formats (`cyclonedx`, `spdx-json`, `spdx`). Both reuse existing `pkg/image` patterns.

**Tech Stack:** existing `pkg/image`, `pkg/storage`, `pkg/ref`; Trivy (already present for `scan`); `gopkg.in/yaml.v3`

---

## File Structure

```
pkg/image/
  bucketconfig.go   ← add PolicyRule, PolicyCheck types, Policies []PolicyRule to BucketConfig
  validate.go       ← NEW: Validate() function and ValidateResult/PolicyResult types
  sbom.go           ← NEW: SBOM() function and SBOMOptions type

cmd/s3lo/
  config.go         ← add configValidateCmd subcommand + applyPolicyKV helper
  sbom.go           ← NEW: sbomCmd cobra command

docs/commands/
  config.md         ← add validate subcommand section
  sbom.md           ← NEW: sbom command docs
```

---

## Task 1: Add PolicyRule types to BucketConfig + `config set` support

**Files:**
- Modify: `pkg/image/bucketconfig.go`

### What to add

`PolicyRule` and `PolicyCheck` types, and `Policies []PolicyRule` in `BucketConfig`:

```go
// PolicyCheck identifies the kind of check a policy performs.
type PolicyCheck string

const (
    PolicyCheckScan   PolicyCheck = "scan"   // run trivy; fail if severity exceeded
    PolicyCheckAge    PolicyCheck = "age"    // fail if image is older than max_days
    PolicyCheckSigned PolicyCheck = "signed" // fail if no cosign signature found
    PolicyCheckSize   PolicyCheck = "size"   // fail if total image size exceeds max_bytes
)

// PolicyRule is a single policy check stored in s3lo.yaml under the `policies` key.
type PolicyRule struct {
    Name        string      `yaml:"name" json:"name"`
    Check       PolicyCheck `yaml:"check" json:"check"`
    // For PolicyCheckScan: severity threshold (e.g. "HIGH"). Trivy exits non-zero
    // when any finding meets or exceeds this level. Valid: LOW, MEDIUM, HIGH, CRITICAL.
    MaxSeverity string      `yaml:"max_severity,omitempty" json:"max_severity,omitempty"`
    // For PolicyCheckAge: maximum age in days.
    MaxDays     int         `yaml:"max_days,omitempty" json:"max_days,omitempty"`
    // For PolicyCheckSize: maximum total image size in bytes.
    MaxBytes    int64       `yaml:"max_bytes,omitempty" json:"max_bytes,omitempty"`
}
```

Add `Policies []PolicyRule` to `BucketConfig`:

```go
type BucketConfig struct {
    Default  ImageConfig            `yaml:"default,omitempty" json:"default,omitempty"`
    Images   map[string]ImageConfig `yaml:"images,omitempty" json:"images,omitempty"`
    Policies []PolicyRule           `yaml:"policies,omitempty" json:"policies,omitempty"`
}
```

- [ ] **Step 1: Write the failing test**

In `pkg/image/bucketconfig_test.go` (create if it doesn't exist):

```go
func TestBucketConfigPoliciesRoundtrip(t *testing.T) {
    yaml := `
policies:
  - name: no-critical-vulns
    check: scan
    max_severity: HIGH
  - name: max-age
    check: age
    max_days: 90
  - name: require-signature
    check: signed
  - name: max-size
    check: size
    max_bytes: 1073741824
`
    var cfg BucketConfig
    if err := yaml2.Unmarshal([]byte(yaml), &cfg); err != nil {
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
    if cfg.Policies[3].MaxBytes != 1073741824 {
        t.Errorf("unexpected max_bytes: %d", cfg.Policies[3].MaxBytes)
    }
}
```

The import alias for yaml in that file: `yaml2 "gopkg.in/yaml.v3"` (since the package is `image`, not a collision).

- [ ] **Step 2: Run to verify it fails**

```bash
cd ~/.config/superpowers/worktrees/s3lo/v1.11.0
go test ./pkg/image/... -run TestBucketConfigPoliciesRoundtrip -v
```

Expected: FAIL — `PolicyRule`, `PolicyCheckScan`, `BucketConfig.Policies` not defined.

- [ ] **Step 3: Add types to `pkg/image/bucketconfig.go`**

After the existing `BucketConfig` struct definition, add:

```go
// PolicyCheck identifies the kind of check a policy performs.
type PolicyCheck string

const (
	PolicyCheckScan   PolicyCheck = "scan"
	PolicyCheckAge    PolicyCheck = "age"
	PolicyCheckSigned PolicyCheck = "signed"
	PolicyCheckSize   PolicyCheck = "size"
)

// PolicyRule is a single policy check stored in s3lo.yaml under the `policies` key.
type PolicyRule struct {
	Name        string      `yaml:"name" json:"name"`
	Check       PolicyCheck `yaml:"check" json:"check"`
	MaxSeverity string      `yaml:"max_severity,omitempty" json:"max_severity,omitempty"`
	MaxDays     int         `yaml:"max_days,omitempty" json:"max_days,omitempty"`
	MaxBytes    int64       `yaml:"max_bytes,omitempty" json:"max_bytes,omitempty"`
}
```

Add `Policies []PolicyRule` to `BucketConfig`:

```go
type BucketConfig struct {
	Default  ImageConfig            `yaml:"default,omitempty" json:"default,omitempty"`
	Images   map[string]ImageConfig `yaml:"images,omitempty" json:"images,omitempty"`
	Policies []PolicyRule           `yaml:"policies,omitempty" json:"policies,omitempty"`
}
```

- [ ] **Step 4: Run to verify it passes**

```bash
go test ./pkg/image/... -run TestBucketConfigPoliciesRoundtrip -v
```

Expected: PASS.

- [ ] **Step 5: Run full test suite**

```bash
go test ./...
```

Expected: all packages pass.

- [ ] **Step 6: Commit**

```bash
git add pkg/image/bucketconfig.go pkg/image/bucketconfig_test.go
git commit -m "feat: add PolicyRule types to BucketConfig"
```

---

## Task 2: Implement `pkg/image/validate.go`

**Files:**
- Create: `pkg/image/validate.go`
- Create: `pkg/image/validate_test.go`

### Context

`Validate` runs each policy in `BucketConfig.Policies` against a specific image tag. It returns a `ValidateResult` with per-policy results and a top-level `AllPassed` bool.

For the `scan` check: download the image to a temp dir (reuse `pullV110` + `finalizeOCILayout` from `scan.go`), invoke trivy with `--exit-code 1 --severity <max_severity>`, check exit code.

For the `age` check: read `manifests/<image>/<tag>/history.json` (same as `pkg/image/history.go`), use the most recent `pushed_at` timestamp.

For the `signed` check: list objects at `manifests/<image>/<tag>/signatures/` — if any `.json` file exists, the image is signed.

For the `size` check: call `Inspect` (already in `pkg/image/inspect.go`) and sum `TotalSize`.

### Types

```go
// ValidateOptions controls policy validation behavior.
type ValidateOptions struct {
	// TrivyPath is the path to the trivy binary (required for scan checks).
	TrivyPath string
}

// PolicyResult holds the result of a single policy check.
type PolicyResult struct {
	Name    string `json:"name"`
	Check   string `json:"check"`
	Passed  bool   `json:"passed"`
	Message string `json:"message"` // human-readable, set on failure
}

// ValidateResult holds the aggregate result of all policy checks.
type ValidateResult struct {
	Reference string         `json:"reference"`
	Results   []PolicyResult `json:"results"`
	AllPassed bool           `json:"all_passed"`
}
```

### `Validate` function signature

```go
func Validate(ctx context.Context, s3Ref string, opts ValidateOptions) (*ValidateResult, error)
```

### Implementation outline

```go
func Validate(ctx context.Context, s3Ref string, opts ValidateOptions) (*ValidateResult, error) {
    parsed, err := ref.Parse(s3Ref)
    if err != nil {
        return nil, fmt.Errorf("invalid reference: %w", err)
    }

    client, err := storage.NewBackendFromRef(ctx, s3Ref)
    if err != nil {
        return nil, fmt.Errorf("create storage client: %w", err)
    }

    cfg, err := GetBucketConfig(ctx, client, parsed.Bucket)
    if err != nil {
        return nil, fmt.Errorf("load bucket config: %w", err)
    }

    result := &ValidateResult{Reference: s3Ref, AllPassed: true}

    for _, policy := range cfg.Policies {
        pr, err := runPolicy(ctx, client, parsed, policy, opts)
        if err != nil {
            return nil, fmt.Errorf("policy %q: %w", policy.Name, err)
        }
        result.Results = append(result.Results, pr)
        if !pr.Passed {
            result.AllPassed = false
        }
    }

    return result, nil
}
```

### `runPolicy` for each check type

```go
func runPolicy(ctx context.Context, client storage.Backend, parsed *ref.Reference, policy PolicyRule, opts ValidateOptions) (PolicyResult, error) {
    switch policy.Check {
    case PolicyCheckScan:
        return runScanPolicy(ctx, parsed, policy, opts.TrivyPath)
    case PolicyCheckAge:
        return runAgePolicy(ctx, client, parsed, policy)
    case PolicyCheckSigned:
        return runSignedPolicy(ctx, client, parsed, policy)
    case PolicyCheckSize:
        return runSizePolicy(ctx, parsed, policy)
    default:
        return PolicyResult{}, fmt.Errorf("unknown check type %q", policy.Check)
    }
}
```

**`runScanPolicy`**: download image to temp dir (call `Scan` from `scan.go` with `opts.TrivyPath`, `MaxSeverity`). If exit code != 0 → failed.

```go
func runScanPolicy(ctx context.Context, parsed *ref.Reference, policy PolicyRule, trivyPath string) (PolicyResult, error) {
    pr := PolicyResult{Name: policy.Name, Check: string(policy.Check)}
    exitCode, err := Scan(ctx, parsed.String(), ScanOptions{
        TrivyPath: trivyPath,
        Severity:  policy.MaxSeverity,
        Format:    "table",
    })
    if err != nil {
        return pr, err
    }
    if exitCode != 0 {
        pr.Message = fmt.Sprintf("vulnerabilities found at or above %s severity", policy.MaxSeverity)
    } else {
        pr.Passed = true
    }
    return pr, nil
}
```

Note: `ref.Reference` needs a `String()` method — check if it exists in `pkg/ref/parse.go`. If not, reconstruct using `parsed.Scheme + "://" + parsed.Bucket + "/" + parsed.Image + ":" + parsed.Tag`.

**`runAgePolicy`**: read `history.json`, check most recent `pushed_at`:

```go
func runAgePolicy(ctx context.Context, client storage.Backend, parsed *ref.Reference, policy PolicyRule) (PolicyResult, error) {
    pr := PolicyResult{Name: policy.Name, Check: string(policy.Check)}
    histKey := parsed.ManifestsPrefix() + "history.json"
    data, err := client.GetObject(ctx, parsed.Bucket, histKey)
    if err != nil {
        if storage.IsNotFound(err) {
            pr.Message = "no push history found for image"
            return pr, nil
        }
        return pr, err
    }
    var entries []HistoryEntry
    if err := json.Unmarshal(data, &entries); err != nil {
        return pr, fmt.Errorf("parse history: %w", err)
    }
    if len(entries) == 0 {
        pr.Message = "no push history found for image"
        return pr, nil
    }
    // Most recent push is first (see history.go ListTagHistory — entries sorted desc by PushedAt).
    latest := entries[0].PushedAt
    ageDays := int(time.Since(latest).Hours() / 24)
    if ageDays > policy.MaxDays {
        pr.Message = fmt.Sprintf("image is %d days old, limit is %d", ageDays, policy.MaxDays)
    } else {
        pr.Passed = true
    }
    return pr, nil
}
```

**`runSignedPolicy`**: check if any `.json` file exists under `manifests/<image>/<tag>/signatures/`:

```go
func runSignedPolicy(ctx context.Context, client storage.Backend, parsed *ref.Reference, policy PolicyRule) (PolicyResult, error) {
    pr := PolicyResult{Name: policy.Name, Check: string(policy.Check)}
    sigPrefix := parsed.ManifestsPrefix() + "signatures/"
    keys, err := client.ListKeys(ctx, parsed.Bucket, sigPrefix)
    if err != nil {
        return pr, fmt.Errorf("list signatures: %w", err)
    }
    if len(keys) == 0 {
        pr.Message = "no signature found"
    } else {
        pr.Passed = true
        pr.Message = fmt.Sprintf("signed (%d signature(s))", len(keys))
    }
    return pr, nil
}
```

**`runSizePolicy`**: call `Inspect` and check `TotalSize`:

```go
func runSizePolicy(ctx context.Context, parsed *ref.Reference, policy PolicyRule) (PolicyResult, error) {
    pr := PolicyResult{Name: policy.Name, Check: string(policy.Check)}
    info, err := Inspect(ctx, parsed.String())
    if err != nil {
        return pr, err
    }
    totalSize := info.TotalSize
    if info.IsIndex {
        for _, p := range info.Platforms {
            if p.TotalSize > totalSize {
                totalSize = p.TotalSize
            }
        }
    }
    if totalSize > policy.MaxBytes {
        pr.Message = fmt.Sprintf("image size %d bytes exceeds limit %d bytes", totalSize, policy.MaxBytes)
    } else {
        pr.Passed = true
    }
    return pr, nil
}
```

- [ ] **Step 1: Write the failing test**

Create `pkg/image/validate_test.go`.

The test pattern for local storage comes from `sign_test.go`: create a temp dir, `os.Chdir` into it, and use a `./mystore` relative path (the local:// backend requires `./` or `../` prefix paths). A minimal OCI manifest is a JSON object with `schemaVersion`, `mediaType`, and empty `layers` array.

```go
package image

import (
    "context"
    "encoding/json"
    "os"
    "path/filepath"
    "testing"
    "time"
)

// makeLocalFixture creates a minimal image fixture under storeDir/manifests/image/tag/
// and returns the local:// ref string. Call os.Chdir(parentDir) before using the ref.
func makeLocalFixture(t *testing.T, parentDir, storeName, imageName, tag string) string {
    t.Helper()
    manifestDir := filepath.Join(parentDir, storeName, "manifests", imageName, tag)
    if err := os.MkdirAll(manifestDir, 0o755); err != nil {
        t.Fatal(err)
    }
    manifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855","size":0},"layers":[]}`)
    if err := os.WriteFile(filepath.Join(manifestDir, "manifest.json"), manifest, 0o644); err != nil {
        t.Fatal(err)
    }
    return "local://./" + storeName + "/" + imageName + ":" + tag
}

func TestValidateNoPoliciesPasses(t *testing.T) {
    ctx := context.Background()
    parentDir := t.TempDir()
    oldCwd, _ := os.Getwd()
    _ = os.Chdir(parentDir)
    defer os.Chdir(oldCwd)

    ref := makeLocalFixture(t, parentDir, "mystore", "myapp", "v1.0")

    // Create an empty BucketConfig (no policies).
    storeDir := filepath.Join(parentDir, "mystore")
    client := NewLocalClient(storeDir)
    cfg := &BucketConfig{}
    if err := SetBucketConfig(ctx, client, storeDir, cfg); err != nil {
        t.Fatal(err)
    }

    result, err := Validate(ctx, ref, ValidateOptions{})
    if err != nil {
        t.Fatal(err)
    }
    if !result.AllPassed {
        t.Errorf("expected AllPassed=true with no policies")
    }
    if len(result.Results) != 0 {
        t.Errorf("expected 0 results, got %d", len(result.Results))
    }
}

func TestValidateAgePolicy_fail(t *testing.T) {
    ctx := context.Background()
    parentDir := t.TempDir()
    oldCwd, _ := os.Getwd()
    _ = os.Chdir(parentDir)
    defer os.Chdir(oldCwd)

    ref := makeLocalFixture(t, parentDir, "mystore", "myapp", "v1.0")
    storeDir := filepath.Join(parentDir, "mystore")
    client := NewLocalClient(storeDir)

    // Write history with a push 200 days ago.
    oldTime := time.Now().AddDate(0, 0, -200)
    entries := []HistoryEntry{{PushedAt: oldTime, Digest: "sha256:abc", SizeBytes: 1000}}
    data, _ := json.Marshal(entries)
    _ = client.PutObject(ctx, storeDir, "manifests/myapp/v1.0/history.json", data)

    cfg := &BucketConfig{
        Policies: []PolicyRule{{Name: "max-age", Check: PolicyCheckAge, MaxDays: 90}},
    }
    if err := SetBucketConfig(ctx, client, storeDir, cfg); err != nil {
        t.Fatal(err)
    }

    result, err := Validate(ctx, ref, ValidateOptions{})
    if err != nil {
        t.Fatal(err)
    }
    if result.AllPassed {
        t.Error("expected AllPassed=false for 200-day-old image with 90-day limit")
    }
    if len(result.Results) != 1 || result.Results[0].Passed {
        t.Errorf("expected 1 failing result, got %+v", result.Results)
    }
}

func TestValidateSignedPolicy_noSignature(t *testing.T) {
    ctx := context.Background()
    parentDir := t.TempDir()
    oldCwd, _ := os.Getwd()
    _ = os.Chdir(parentDir)
    defer os.Chdir(oldCwd)

    ref := makeLocalFixture(t, parentDir, "mystore", "myapp", "v1.0")
    storeDir := filepath.Join(parentDir, "mystore")
    client := NewLocalClient(storeDir)

    cfg := &BucketConfig{
        Policies: []PolicyRule{{Name: "require-signature", Check: PolicyCheckSigned}},
    }
    if err := SetBucketConfig(ctx, client, storeDir, cfg); err != nil {
        t.Fatal(err)
    }

    result, err := Validate(ctx, ref, ValidateOptions{})
    if err != nil {
        t.Fatal(err)
    }
    if result.AllPassed {
        t.Error("expected AllPassed=false when no signature present")
    }
    if result.Results[0].Message == "" {
        t.Error("expected non-empty failure message")
    }
}

func TestValidateSizePolicy_pass(t *testing.T) {
    ctx := context.Background()
    parentDir := t.TempDir()
    oldCwd, _ := os.Getwd()
    _ = os.Chdir(parentDir)
    defer os.Chdir(oldCwd)

    ref := makeLocalFixture(t, parentDir, "mystore", "myapp", "v1.0")
    storeDir := filepath.Join(parentDir, "mystore")
    client := NewLocalClient(storeDir)

    cfg := &BucketConfig{
        Policies: []PolicyRule{{Name: "max-size", Check: PolicyCheckSize, MaxBytes: 1 << 30}},
    }
    if err := SetBucketConfig(ctx, client, storeDir, cfg); err != nil {
        t.Fatal(err)
    }

    result, err := Validate(ctx, ref, ValidateOptions{})
    if err != nil {
        t.Fatal(err)
    }
    // Minimal image has 0 bytes of layers — well under 1 GB.
    if !result.AllPassed {
        t.Errorf("expected AllPassed=true, got failure: %+v", result.Results)
    }
}

- [ ] **Step 2: Run to verify tests fail**

```bash
cd ~/.config/superpowers/worktrees/s3lo/v1.11.0
go test ./pkg/image/... -run "TestValidate" -v
```

Expected: FAIL — `Validate`, `ValidateOptions`, `ValidateResult`, `PolicyResult` not defined.

- [ ] **Step 3: Create `pkg/image/validate.go`**

Full implementation (incorporating all the code shown above in this task's context section):

```go
package image

import (
    "context"
    "encoding/json"
    "fmt"
    "time"

    "github.com/OuFinx/s3lo/pkg/ref"
    storage "github.com/OuFinx/s3lo/pkg/storage"
)

// ValidateOptions controls policy validation behavior.
type ValidateOptions struct {
    // TrivyPath is the path to the trivy binary (required for scan checks).
    TrivyPath string
}

// PolicyResult holds the result of a single policy check.
type PolicyResult struct {
    Name    string `json:"name"`
    Check   string `json:"check"`
    Passed  bool   `json:"passed"`
    Message string `json:"message,omitempty"`
}

// ValidateResult holds the aggregate result of all policy checks.
type ValidateResult struct {
    Reference string         `json:"reference"`
    Results   []PolicyResult `json:"results"`
    AllPassed bool           `json:"all_passed"`
}

// Validate runs all policies in the bucket's s3lo.yaml against the given image tag.
// Returns a ValidateResult with per-policy outcomes and AllPassed=true only when
// every policy passes. Scan checks require opts.TrivyPath.
func Validate(ctx context.Context, s3Ref string, opts ValidateOptions) (*ValidateResult, error) {
    parsed, err := ref.Parse(s3Ref)
    if err != nil {
        return nil, fmt.Errorf("invalid reference: %w", err)
    }

    client, err := storage.NewBackendFromRef(ctx, s3Ref)
    if err != nil {
        return nil, fmt.Errorf("create storage client: %w", err)
    }

    cfg, err := GetBucketConfig(ctx, client, parsed.Bucket)
    if err != nil {
        return nil, fmt.Errorf("load bucket config: %w", err)
    }

    result := &ValidateResult{Reference: s3Ref, AllPassed: true}
    if len(cfg.Policies) == 0 {
        return result, nil
    }

    for _, policy := range cfg.Policies {
        pr, err := runPolicy(ctx, client, parsed, policy, opts)
        if err != nil {
            return nil, fmt.Errorf("policy %q: %w", policy.Name, err)
        }
        result.Results = append(result.Results, pr)
        if !pr.Passed {
            result.AllPassed = false
        }
    }

    return result, nil
}

func runPolicy(ctx context.Context, client storage.Backend, parsed *ref.Reference, policy PolicyRule, opts ValidateOptions) (PolicyResult, error) {
    switch policy.Check {
    case PolicyCheckScan:
        return runScanPolicy(ctx, parsed, policy, opts.TrivyPath)
    case PolicyCheckAge:
        return runAgePolicy(ctx, client, parsed, policy)
    case PolicyCheckSigned:
        return runSignedPolicy(ctx, client, parsed, policy)
    case PolicyCheckSize:
        return runSizePolicy(ctx, parsed, policy)
    default:
        return PolicyResult{}, fmt.Errorf("unknown check type %q", policy.Check)
    }
}

func runScanPolicy(ctx context.Context, parsed *ref.Reference, policy PolicyRule, trivyPath string) (PolicyResult, error) {
    pr := PolicyResult{Name: policy.Name, Check: string(policy.Check)}
    if trivyPath == "" {
        return pr, fmt.Errorf("TrivyPath required for scan policy")
    }
    exitCode, err := Scan(ctx, parsed.String(), ScanOptions{
        TrivyPath: trivyPath,
        Severity:  policy.MaxSeverity,
        Format:    "table",
    })
    if err != nil {
        return pr, err
    }
    if exitCode != 0 {
        pr.Message = fmt.Sprintf("vulnerabilities found at or above %s severity", policy.MaxSeverity)
    } else {
        pr.Passed = true
    }
    return pr, nil
}

func runAgePolicy(ctx context.Context, client storage.Backend, parsed *ref.Reference, policy PolicyRule) (PolicyResult, error) {
    pr := PolicyResult{Name: policy.Name, Check: string(policy.Check)}
    histKey := parsed.ManifestsPrefix() + "history.json"
    data, err := client.GetObject(ctx, parsed.Bucket, histKey)
    if err != nil {
        if storage.IsNotFound(err) {
            pr.Message = "no push history found"
            return pr, nil
        }
        return pr, err
    }
    var entries []HistoryEntry
    if err := json.Unmarshal(data, &entries); err != nil {
        return pr, fmt.Errorf("parse history: %w", err)
    }
    if len(entries) == 0 {
        pr.Message = "no push history found"
        return pr, nil
    }
    latest := entries[0].PushedAt
    ageDays := int(time.Since(latest).Hours() / 24)
    if ageDays > policy.MaxDays {
        pr.Message = fmt.Sprintf("image is %d days old, limit is %d", ageDays, policy.MaxDays)
    } else {
        pr.Passed = true
    }
    return pr, nil
}

func runSignedPolicy(ctx context.Context, client storage.Backend, parsed *ref.Reference, policy PolicyRule) (PolicyResult, error) {
    pr := PolicyResult{Name: policy.Name, Check: string(policy.Check)}
    sigPrefix := parsed.ManifestsPrefix() + "signatures/"
    keys, err := client.ListKeys(ctx, parsed.Bucket, sigPrefix)
    if err != nil {
        return pr, fmt.Errorf("list signatures: %w", err)
    }
    if len(keys) == 0 {
        pr.Message = "no signature found"
    } else {
        pr.Passed = true
        pr.Message = fmt.Sprintf("signed (%d signature(s))", len(keys))
    }
    return pr, nil
}

func runSizePolicy(ctx context.Context, parsed *ref.Reference, policy PolicyRule) (PolicyResult, error) {
    pr := PolicyResult{Name: policy.Name, Check: string(policy.Check)}
    info, err := Inspect(ctx, parsed.String())
    if err != nil {
        return pr, err
    }
    totalSize := info.TotalSize
    if info.IsIndex {
        for _, p := range info.Platforms {
            if p.TotalSize > totalSize {
                totalSize = p.TotalSize
            }
        }
    }
    if totalSize > policy.MaxBytes {
        pr.Message = fmt.Sprintf("image size %d bytes exceeds limit %d bytes", totalSize, policy.MaxBytes)
    } else {
        pr.Passed = true
    }
    return pr, nil
}
```

Also check `pkg/ref/parse.go` for `Reference.String()` — if it doesn't exist, add it:

```go
// String returns the canonical string representation of the reference.
func (r *Reference) String() string {
    return r.Scheme + "://" + r.Bucket + "/" + r.Image + ":" + r.Tag
}
```

- [ ] **Step 4: Run to verify tests pass**

```bash
go test ./pkg/image/... -run "TestValidate" -v
```

Expected: all 4 TestValidate* tests PASS.

- [ ] **Step 5: Run full test suite**

```bash
go test ./...
```

Expected: all packages pass.

- [ ] **Step 6: Commit**

```bash
git add pkg/image/validate.go pkg/image/validate_test.go pkg/ref/parse.go
git commit -m "feat: implement Validate() with age, signed, size, and scan policy checks"
```

---

## Task 3: Add `s3lo config validate` CLI subcommand

**Files:**
- Modify: `cmd/s3lo/config.go`

### What to add

A new `configValidateCmd` cobra command that:
1. Parses the s3-ref (must include a tag)
2. Calls `ensureTrivy` if any scan policies exist (reuse from `scan.go`)
3. Calls `image.Validate`
4. Prints results in a human-readable table
5. Exits 1 if any policy failed

Example output (matching the issue):

```
✓ no-critical-vulns    passed
✗ max-age              FAILED (image is 127 days old, limit is 90)
✓ require-signature    passed (sha256:abc123)

1 policy failed.
```

- [ ] **Step 1: Write the failing test**

Tests for CLI commands live in the `cmd/s3lo` package. That package has no test files currently — this is just a visual regression. We validate the command wires up correctly with a compile check. Add a small test to `pkg/image/validate_test.go` instead that covers the output format logic:

```go
func TestPolicyResultMessage(t *testing.T) {
    pr := PolicyResult{Name: "max-age", Check: "age", Passed: false, Message: "image is 127 days old, limit is 90"}
    if pr.Passed {
        t.Error("should not be passed")
    }
    if pr.Message == "" {
        t.Error("expected non-empty message")
    }
}
```

- [ ] **Step 2: Run to verify test passes (it's trivial)**

```bash
go test ./pkg/image/... -run TestPolicyResultMessage -v
```

Expected: PASS (no code needed, just compile).

- [ ] **Step 3: Add `configValidateCmd` to `cmd/s3lo/config.go`**

Add before the `init()` function:

```go
var configValidateCmd = &cobra.Command{
    Use:   "validate <s3-ref>",
    Short: "Run policy checks against an image",
    Long: `Run all policies defined in s3lo.yaml against the given image tag.

Exits 0 when all policies pass, 1 when one or more policies fail.`,
    Example: `  Docs: https://oufinx.github.io/s3lo/commands/config/

  s3lo config validate s3://my-bucket/myapp:v1.0`,
    Args: cobra.ExactArgs(1),
    RunE: func(cmd *cobra.Command, args []string) error {
        if err := requireTag(args[0]); err != nil {
            return err
        }

        // Check if any scan policies exist — need trivy.
        // We resolve trivy lazily: load config, check if scan policies present.
        client, err := storage.NewBackendFromRef(cmd.Context(), args[0])
        if err != nil {
            return err
        }
        bucket, _, err := image.ParseConfigRef(args[0])
        if err != nil {
            return err
        }
        cfg, err := image.GetBucketConfig(cmd.Context(), client, bucket)
        if err != nil {
            return err
        }

        var trivyPath string
        for _, p := range cfg.Policies {
            if p.Check == image.PolicyCheckScan {
                installFlag, _ := cmd.Flags().GetBool("install-trivy")
                trivyPath, err = ensureTrivy(cmd.Context(), installFlag)
                if err != nil {
                    return err
                }
                break
            }
        }

        if len(cfg.Policies) == 0 {
            fmt.Println("No policies configured.")
            return nil
        }

        result, err := image.Validate(cmd.Context(), args[0], image.ValidateOptions{
            TrivyPath: trivyPath,
        })
        if err != nil {
            return err
        }

        // Print results.
        failCount := 0
        for _, r := range result.Results {
            if r.Passed {
                if r.Message != "" {
                    fmt.Printf("✓ %-25s passed (%s)\n", r.Name, r.Message)
                } else {
                    fmt.Printf("✓ %-25s passed\n", r.Name)
                }
            } else {
                fmt.Printf("✗ %-25s FAILED (%s)\n", r.Name, r.Message)
                failCount++
            }
        }
        fmt.Println()
        if failCount > 0 {
            fmt.Printf("%d policy failed.\n", failCount)
            os.Exit(1)
        }
        fmt.Println("All policies passed.")
        return nil
    },
}
```

Add to `init()` in `config.go`:

```go
configValidateCmd.Flags().Bool("install-trivy", false, "Install Trivy automatically without prompting (for scan policies)")
configCmd.AddCommand(configValidateCmd)
```

Make sure `"os"` is in the imports for `config.go`.

- [ ] **Step 4: Build to verify compilation**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 5: Run full test suite**

```bash
go test ./...
```

Expected: all packages pass.

- [ ] **Step 6: Commit**

```bash
git add cmd/s3lo/config.go pkg/image/validate_test.go
git commit -m "feat: add s3lo config validate subcommand"
```

---

## Task 4: Implement `pkg/image/sbom.go`

**Files:**
- Create: `pkg/image/sbom.go`
- Create: `pkg/image/sbom_test.go`

### Context

`s3lo sbom` is almost identical to `s3lo scan` — both:
1. Download the image to a temp OCI layout directory (calls `pullV110` + `finalizeOCILayout`)
2. Invoke trivy

The difference: `sbom` always uses a SBOM format (`cyclonedx`, `spdx-json`, `spdx`) and can write to a file via `--output/-o`. It does not do vulnerability scanning — trivy is invoked differently:

```
trivy image --input <dir> --format cyclonedx --output -
```

vs the scan command:
```
trivy image --input <dir> --severity HIGH,CRITICAL --exit-code 1
```

### Types and function

```go
// SBOMOptions controls SBOM generation behavior.
type SBOMOptions struct {
    // Format is the SBOM output format: "cyclonedx" (default), "spdx-json", "spdx".
    Format string
    // Platform selects a specific platform from a multi-arch image.
    Platform string
    // OutputPath writes the SBOM to a file instead of stdout. Empty means stdout.
    OutputPath string
    // TrivyPath is the absolute path to the trivy binary.
    TrivyPath string
    // OnStart is called once with the total blob bytes before downloads begin.
    OnStart func(totalBytes int64)
    // OnBlob is called after each blob is downloaded.
    OnBlob func(digest string, size int64)
}

// SBOM generates a Software Bill of Materials for an image stored in object storage.
// Output is written to opts.OutputPath or stdout if empty.
func SBOM(ctx context.Context, s3Ref string, opts SBOMOptions) error
```

### Implementation

```go
func SBOM(ctx context.Context, s3Ref string, opts SBOMOptions) error {
    if opts.Format == "" {
        opts.Format = "cyclonedx"
    }

    parsed, err := ref.Parse(s3Ref)
    if err != nil {
        return fmt.Errorf("invalid reference: %w", err)
    }

    client, err := storage.NewBackendFromRef(ctx, s3Ref)
    if err != nil {
        return fmt.Errorf("create storage client: %w", err)
    }

    tmpDir, err := os.MkdirTemp("", "s3lo-sbom-*")
    if err != nil {
        return fmt.Errorf("create temp dir: %w", err)
    }
    defer os.RemoveAll(tmpDir)

    // Fetch manifest.
    manifestKey := parsed.ManifestsPrefix() + "manifest.json"
    manifestData, err := client.GetObject(ctx, parsed.Bucket, manifestKey)
    if err != nil {
        return fmt.Errorf("fetch manifest: %w", err)
    }

    // Resolve platform for multi-arch images.
    if isImageIndex(manifestData) {
        manifestData, err = resolvePlatformManifest(ctx, client, parsed.Bucket, manifestData, opts.Platform)
        if err != nil {
            return err
        }
    }

    // Download blobs.
    if err := pullV110(ctx, client, parsed, manifestData, tmpDir, opts.OnBlob, opts.OnStart); err != nil {
        return fmt.Errorf("download image: %w", err)
    }

    // Build OCI Image Layout.
    if err := finalizeOCILayout(tmpDir, manifestData); err != nil {
        return fmt.Errorf("build OCI layout: %w", err)
    }

    // Build trivy arguments.
    args := []string{"image", "--input", tmpDir, "--format", opts.Format}

    cmd := exec.CommandContext(ctx, opts.TrivyPath, args...)
    if opts.OutputPath != "" {
        f, err := os.Create(opts.OutputPath)
        if err != nil {
            return fmt.Errorf("create output file: %w", err)
        }
        defer f.Close()
        cmd.Stdout = f
    } else {
        cmd.Stdout = os.Stdout
    }
    cmd.Stderr = os.Stderr

    if err := cmd.Run(); err != nil {
        if _, ok := err.(*exec.ExitError); ok {
            // Trivy exiting non-zero during SBOM generation is unusual but not fatal.
            return fmt.Errorf("trivy exited with error: %w", err)
        }
        return fmt.Errorf("run trivy: %w", err)
    }
    return nil
}
```

Required imports in `sbom.go`: `context`, `fmt`, `os`, `os/exec`, `github.com/OuFinx/s3lo/pkg/ref`, `storage "github.com/OuFinx/s3lo/pkg/storage"`.

- [ ] **Step 1: Write the failing test**

Create `pkg/image/sbom_test.go`. Reuse `makeLocalFixture` from `validate_test.go` (same package):

```go
package image

import (
    "context"
    "os"
    "os/exec"
    "testing"
)

func TestSBOMOptionsDefaults(t *testing.T) {
    opts := SBOMOptions{}
    if opts.Format != "" {
        t.Error("expected empty format before defaulting")
    }
    _ = SBOM // compile check
}

func TestSBOMRequiresTrivyPath(t *testing.T) {
    if _, err := exec.LookPath("trivy"); err == nil {
        t.Skip("trivy found in PATH, skipping error path test")
    }
    ctx := context.Background()
    parentDir := t.TempDir()
    oldCwd, _ := os.Getwd()
    _ = os.Chdir(parentDir)
    defer os.Chdir(oldCwd)

    ref := makeLocalFixture(t, parentDir, "mystore", "myapp", "v1.0")

    err := SBOM(ctx, ref, SBOMOptions{
        TrivyPath: "/nonexistent/trivy",
        Format:    "cyclonedx",
    })
    if err == nil {
        t.Error("expected error when trivy binary does not exist")
    }
}
```

- [ ] **Step 2: Run to verify it fails**

```bash
go test ./pkg/image/... -run "TestSBOM" -v
```

Expected: FAIL — `SBOM` not defined, `SBOMOptions` not defined.

- [ ] **Step 3: Create `pkg/image/sbom.go`**

Write the full implementation as shown above.

- [ ] **Step 4: Run to verify tests pass**

```bash
go test ./pkg/image/... -run "TestSBOM" -v
```

Expected: PASS (TestSBOMOptionsDefaults and TestSBOMRequiresTrivyPath both pass).

- [ ] **Step 5: Run full test suite**

```bash
go test ./...
```

Expected: all packages pass.

- [ ] **Step 6: Commit**

```bash
git add pkg/image/sbom.go pkg/image/sbom_test.go
git commit -m "feat: implement SBOM() for CycloneDX/SPDX generation via Trivy"
```

---

## Task 5: Add `s3lo sbom` CLI command

**Files:**
- Create: `cmd/s3lo/sbom.go`

### Full implementation

```go
package main

import (
    "fmt"

    "github.com/OuFinx/s3lo/pkg/image"
    "github.com/schollz/progressbar/v3"
    "github.com/spf13/cobra"
    "golang.org/x/term"
    "os"
)

var sbomCmd = &cobra.Command{
    Use:   "sbom <s3-ref>",
    Short: "Generate a Software Bill of Materials (SBOM) for an image",
    Long: `Download an image from storage and generate a Software Bill of Materials using Trivy.

Output formats: cyclonedx (default), spdx-json, spdx

Trivy must be installed, or s3lo can install it automatically.
Use --install-trivy to skip the confirmation prompt.`,
    Example: `  Docs: https://oufinx.github.io/s3lo/commands/sbom/

  s3lo sbom s3://my-bucket/myapp:v1.0
  s3lo sbom s3://my-bucket/myapp:v1.0 --format spdx-json
  s3lo sbom s3://my-bucket/myapp:v1.0 --format cyclonedx -o myapp.cdx.json
  s3lo sbom s3://my-bucket/myapp:v1.0 --platform linux/amd64`,
    Args: cobra.ExactArgs(1),
    RunE: func(cmd *cobra.Command, args []string) error {
        if err := requireTag(args[0]); err != nil {
            return err
        }
        installFlag, _ := cmd.Flags().GetBool("install-trivy")
        format, _ := cmd.Flags().GetString("format")
        platform, _ := cmd.Flags().GetString("platform")
        outputPath, _ := cmd.Flags().GetString("output")

        trivyPath, err := ensureTrivy(cmd.Context(), installFlag)
        if err != nil {
            return err
        }

        if outputPath == "" {
            fmt.Fprintf(os.Stderr, "Generating SBOM for %s\n", args[0])
        } else {
            fmt.Printf("Generating SBOM for %s\n", args[0])
        }
        var bar *progressbar.ProgressBar
        opts := image.SBOMOptions{
            Format:     format,
            Platform:   platform,
            OutputPath: outputPath,
            TrivyPath:  trivyPath,
            OnStart: func(total int64) {
                if term.IsTerminal(int(os.Stderr.Fd())) {
                    bar = newProgressBar("  downloading", total)
                }
            },
            OnBlob: func(_ string, size int64) {
                if bar != nil {
                    bar.Add64(size)
                }
            },
        }

        if err := image.SBOM(cmd.Context(), args[0], opts); err != nil {
            return err
        }
        if bar != nil {
            bar.Finish()
        }
        if outputPath != "" {
            fmt.Printf("SBOM written to %s\n", outputPath)
        }
        return nil
    },
}

func init() {
    rootCmd.AddCommand(sbomCmd)
    sbomCmd.Flags().Bool("install-trivy", false, "Install Trivy automatically without prompting")
    sbomCmd.Flags().String("format", "cyclonedx", `SBOM output format: cyclonedx (default), spdx-json, spdx`)
    sbomCmd.Flags().String("platform", "", `Platform for a multi-arch image (e.g. "linux/amd64")`)
    sbomCmd.Flags().StringP("output", "o", "", "Write SBOM to file instead of stdout")
}
```

- [ ] **Step 1: Write the failing test** (compile-time only)

Add to `pkg/image/sbom_test.go`:

```go
func TestSBOMOptionsHasOutputPath(t *testing.T) {
    opts := SBOMOptions{OutputPath: "/tmp/sbom.json"}
    if opts.OutputPath != "/tmp/sbom.json" {
        t.Errorf("unexpected output path: %s", opts.OutputPath)
    }
}
```

- [ ] **Step 2: Run to verify test passes**

```bash
go test ./pkg/image/... -run TestSBOMOptionsHasOutputPath -v
```

Expected: PASS.

- [ ] **Step 3: Create `cmd/s3lo/sbom.go`** with the full code above

- [ ] **Step 4: Build to verify compilation**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 5: Register sbom in mkdocs.yml**

In `mkdocs.yml`, add `sbom: commands/sbom.md` to the Commands nav section:

```yaml
  - Commands:
    - commands/index.md
    - push: commands/push.md
    - pull: commands/pull.md
    - copy: commands/copy.md
    - list: commands/list.md
    - inspect: commands/inspect.md
    - delete: commands/delete.md
    - clean: commands/clean.md
    - stats: commands/stats.md
    - config: commands/config.md
    - history: commands/history.md
    - scan: commands/scan.md
    - sbom: commands/sbom.md        ← add this
    - sign: commands/sign.md
    - verify: commands/verify.md
```

Also add to `docs/commands/index.md` command table:

```markdown
| [`sbom`](sbom.md) | Generate a Software Bill of Materials (SBOM) using Trivy |
```

Create `docs/commands/sbom.md` with the sbom command docs.

- [ ] **Step 6: Run full test suite**

```bash
go test ./...
```

Expected: all packages pass.

- [ ] **Step 7: Commit**

```bash
git add cmd/s3lo/sbom.go mkdocs.yml docs/commands/sbom.md docs/commands/index.md pkg/image/sbom_test.go
git commit -m "feat: add s3lo sbom command for CycloneDX/SPDX SBOM generation"
```

---

## Task 6: Docs + ROADMAP + close GitHub issues

**Files:**
- Modify: `docs/commands/config.md` — add validate subcommand section
- Modify: `ROADMAP.md` — mark v1.11.0 items complete
- Create: PR from v1.11.0 to main
- Close: GitHub issues #52, #53

### What to document in `docs/commands/config.md`

Add a `validate` subsection after the `recommend` section:

````markdown
## validate

Run all policies defined in `s3lo.yaml` against a specific image tag.

```bash
s3lo config validate s3://my-bucket/myapp:v1.0
```

Exits 0 if all policies pass, 1 if any policy fails. Suitable for CI gates.

### Policy configuration

Policies are defined in `s3lo.yaml` under the `policies` key:

```yaml
policies:
  - name: no-critical-vulns
    check: scan
    max_severity: HIGH

  - name: max-age
    check: age
    max_days: 90

  - name: require-signature
    check: signed

  - name: max-size
    check: size
    max_bytes: 1073741824  # 1 GB
```

### Policy checks

| Check | Description | Parameters |
|-------|-------------|------------|
| `scan` | Run Trivy; fail if vulnerabilities exceed severity | `max_severity`: LOW, MEDIUM, HIGH, CRITICAL |
| `age` | Fail if the image is older than N days | `max_days` |
| `signed` | Fail if no cosign signature is present | (none) |
| `size` | Fail if total image size exceeds N bytes | `max_bytes` |

### Example output

```
✓ no-critical-vulns    passed
✗ max-age              FAILED (image is 127 days old, limit is 90)
✓ require-signature    passed (1 signature(s))

1 policy failed.
```

### CI integration

```yaml
- run: s3lo config validate s3://my-bucket/$IMAGE:$SHA
```
````

### `docs/commands/sbom.md`

Create with full usage docs matching the scan.md style.

- [ ] **Step 1: Write docs**

Update `docs/commands/config.md`, create `docs/commands/sbom.md`.

- [ ] **Step 2: Update ROADMAP.md**

Mark v1.11.0 items `[x]`:

```markdown
## v1.11.0 — Policy & SBOM

- [x] `s3lo config validate` — policy rules and compliance checks
- [x] `s3lo sbom` — generate Software Bill of Materials
```

- [ ] **Step 3: Build + test one final time**

```bash
go build ./... && go test ./...
```

Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add docs/commands/config.md docs/commands/sbom.md ROADMAP.md
git commit -m "docs: update config.md and add sbom.md for v1.11.0"
```

- [ ] **Step 5: Push and create PR**

```bash
git push origin v1.11.0
gh pr create --title "v1.11.0: Policy & SBOM" --body "$(cat <<'EOF'
## Summary

- `s3lo config validate` — run policy checks (scan, age, signed, size) against a stored image tag; exits 1 if any policy fails
- `s3lo sbom` — generate CycloneDX or SPDX SBOM from a stored image using Trivy

## Closes

- #52
- #53
EOF
)"
```

- [ ] **Step 6: After merge, close GitHub issues**

```bash
gh issue comment 52 -b "Implemented in v1.11.0. \`s3lo config validate\` runs all policies defined in \`s3lo.yaml\` (scan, age, signed, size checks) against an image tag. Exits 0/1 for CI gates."
gh issue close 52
gh issue comment 53 -b "Implemented in v1.11.0. \`s3lo sbom\` generates CycloneDX or SPDX SBOMs via Trivy from images stored in any supported backend. Supports --format, --platform, and -o flags."
gh issue close 53
```

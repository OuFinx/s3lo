# scan

Download an image from S3 and scan it for vulnerabilities using [Trivy](https://trivy.dev).

```
s3lo scan <s3-ref> [flags]
```

The reference must include an explicit tag. Both `s3://` and `local://` references are supported.

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--severity` | all | Comma-separated severity levels to report: `LOW`, `MEDIUM`, `HIGH`, `CRITICAL` |
| `--format` | `table` | Output format: `table`, `json`, `sarif`, `cyclonedx` |
| `--platform` | host | Platform to scan from a multi-arch image (e.g. `linux/amd64`) |
| `--install-trivy` | false | Install Trivy automatically without prompting |

## Trivy auto-install

If Trivy is not found on `PATH` or in `~/.local/bin/`, s3lo prompts you:

```
Trivy is not installed. Install it now to ~/.local/bin/trivy? [Y/n]
```

Press **Enter** or **Y** to install. The latest Trivy release is downloaded from GitHub for your OS and architecture.

In CI (non-TTY), the prompt is skipped — s3lo exits with an error and suggests `--install-trivy`:

```
trivy not found — install it (https://trivy.dev) or run with --install-trivy to auto-install
```

Pass `--install-trivy` in CI to enable auto-install:

```yaml
- run: s3lo scan s3://my-bucket/myapp:v1.0 --severity HIGH,CRITICAL --install-trivy
```

## Examples

**Basic scan (all severities):**

```bash
s3lo scan s3://my-bucket/myapp:v1.0
```

**Fail only on HIGH or CRITICAL:**

```bash
s3lo scan s3://my-bucket/myapp:v1.0 --severity HIGH,CRITICAL
```

**JSON output (for CI pipeline integration):**

```bash
s3lo scan s3://my-bucket/myapp:v1.0 --format json > results.json
```

**SARIF output (for GitHub Security tab):**

```bash
s3lo scan s3://my-bucket/myapp:v1.0 --format sarif > trivy-results.sarif
```

**Scan a specific platform from a multi-arch image:**

```bash
s3lo scan s3://my-bucket/alpine:latest --platform linux/arm64
```

## Output

=== "Table (default)"

    ```
    Scanning s3://my-bucket/myapp:v1.0
      downloading ⠸ 58.70 MB

    myapp:v1.0 (debian 12.9)
    ========================
    Total: 3 (MEDIUM: 2, HIGH: 1)

    ┌──────────────┬───────────────┬──────────┬───────────────────┬───────────────┬──────────────────────────┐
    │   Library    │ Vulnerability │ Severity │ Installed Version │ Fixed Version │          Title           │
    ├──────────────┼───────────────┼──────────┼───────────────────┼───────────────┼──────────────────────────┤
    │ libssl3      │ CVE-2024-0727 │ HIGH     │ 3.0.11-1~deb12u2  │ 3.0.13-1      │ OpenSSL: denial of       │
    │              │               │          │                   │               │ service in PKCS12        │
    ├──────────────┼───────────────┼──────────┼───────────────────┼───────────────┼──────────────────────────┤
    │ libexpat1    │ CVE-2023-52425│ MEDIUM   │ 2.5.0-1           │ 2.6.0-1       │ expat: parsing large     │
    │              │               │          │                   │               │ tokens                   │
    └──────────────┴───────────────┴──────────┴───────────────────┴───────────────┴──────────────────────────┘
    ```

=== "JSON"

    ```json
    {
      "SchemaVersion": 2,
      "ArtifactName": "/tmp/s3lo-scan-1234.tar",
      "ArtifactType": "container_image",
      "Results": [
        {
          "Target": "myapp:v1.0 (debian 12.9)",
          "Class": "os-pkgs",
          "Type": "debian",
          "Vulnerabilities": [
            {
              "VulnerabilityID": "CVE-2024-0727",
              "PkgName": "libssl3",
              "Severity": "HIGH",
              "InstalledVersion": "3.0.11-1~deb12u2",
              "FixedVersion": "3.0.13-1",
              "Title": "OpenSSL: denial of service in PKCS12"
            }
          ]
        }
      ]
    }
    ```

## Exit codes

| Code | Meaning |
|------|---------|
| `0` | No vulnerabilities at the requested severity level |
| `1` | Vulnerabilities found (use in CI to fail the build) |

## GitHub Actions example

```yaml
- uses: OuFinx/s3lo-action@v1
  with:
    role-to-assume: arn:aws:iam::123456789012:role/ci-s3lo-role
    aws-region: us-east-1

- name: Scan for vulnerabilities
  run: |
    s3lo scan s3://my-bucket/${{ github.repository }}:${{ github.sha }} \
      --severity HIGH,CRITICAL \
      --install-trivy
```

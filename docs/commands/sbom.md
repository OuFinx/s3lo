# sbom

Download an image from storage and generate a Software Bill of Materials using [Trivy](https://trivy.dev).

```
s3lo sbom <s3-ref> [flags]
```

The reference must include an explicit tag. Both `s3://` and `local://` references are supported.

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--format` | `cyclonedx` | SBOM output format: `cyclonedx`, `spdx-json`, `spdx` |
| `--platform` | host | Platform for a multi-arch image (e.g. `linux/amd64`) |
| `--install-trivy` | false | Install Trivy automatically without prompting |
| `-o`, `--output` | stdout | Write SBOM to a file instead of stdout |

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
- run: s3lo sbom s3://my-bucket/myapp:v1.0 -o sbom.cdx.json --install-trivy
```

## Examples

**Basic SBOM (CycloneDX, printed to stdout):**

```bash
s3lo sbom s3://my-bucket/myapp:v1.0
```

**Write CycloneDX SBOM to a file:**

```bash
s3lo sbom s3://my-bucket/myapp:v1.0 -o myapp.cdx.json
```

**SPDX JSON format:**

```bash
s3lo sbom s3://my-bucket/myapp:v1.0 --format spdx-json -o myapp.spdx.json
```

**SPDX tag-value format:**

```bash
s3lo sbom s3://my-bucket/myapp:v1.0 --format spdx -o myapp.spdx
```

**Multi-arch image — select a specific platform:**

```bash
s3lo sbom s3://my-bucket/myapp:v1.0 --platform linux/amd64 -o myapp.cdx.json
```

## Output

When `-o`/`--output` is omitted, the SBOM is written to stdout so it can be piped or redirected:

```bash
s3lo sbom s3://my-bucket/myapp:v1.0 | jq '.metadata.component.name'
```

Progress output (the download bar) is written to stderr so it does not pollute the SBOM data on stdout.

When an output file is specified, both the progress bar and a completion message are written to stdout:

```
Generating SBOM for s3://my-bucket/myapp:v1.0
  downloading ⠸ 58.70 MB
SBOM written to myapp.cdx.json
```

## GitHub Actions example

```yaml
- uses: OuFinx/s3lo-action@v1
  with:
    role-to-assume: arn:aws:iam::123456789012:role/ci-s3lo-role
    aws-region: us-east-1

- name: Generate SBOM
  run: |
    s3lo sbom s3://my-bucket/${{ github.repository }}:${{ github.sha }} \
      --format cyclonedx \
      -o sbom.cdx.json \
      --install-trivy

- name: Upload SBOM artifact
  uses: actions/upload-artifact@v4
  with:
    name: sbom
    path: sbom.cdx.json
```

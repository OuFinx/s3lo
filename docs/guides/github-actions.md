# GitHub Actions

s3lo works seamlessly in GitHub Actions using OIDC — no long-lived AWS credentials needed.

## Setup

### 1. Create an IAM role for GitHub Actions

Create a role with an OIDC trust policy that allows your repository to assume it:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": {
        "Federated": "arn:aws:iam::123456789012:oidc-provider/token.actions.githubusercontent.com"
      },
      "Action": "sts:AssumeRoleWithWebIdentity",
      "Condition": {
        "StringEquals": {
          "token.actions.githubusercontent.com:aud": "sts.amazonaws.com"
        },
        "StringLike": {
          "token.actions.githubusercontent.com:sub": "repo:OuFinx/myapp:*"
        }
      }
    }
  ]
}
```

Attach the [push+pull IAM policy](../reference/iam-policies.md) to the role.

### 2. Add the `id-token` permission

All workflows using OIDC need:

```yaml
permissions:
  id-token: write
  contents: read
```

---

## Build and push on every commit

```yaml
name: Build and Push

on:
  push:
    branches: [main]

jobs:
  push:
    runs-on: ubuntu-latest
    permissions:
      id-token: write
      contents: read

    steps:
      - uses: actions/checkout@v4

      - uses: OuFinx/s3lo-action@v1
        with:
          role-to-assume: arn:aws:iam::123456789012:role/ci-s3lo-role
          aws-region: us-east-1

      - name: Build image
        run: docker build --platform linux/amd64 -t myapp:${{ github.sha }} .

      - name: Push to S3
        run: |
          s3lo push myapp:${{ github.sha }} s3://my-bucket/myapp:${{ github.sha }}
          s3lo copy s3://my-bucket/myapp:${{ github.sha }} s3://my-bucket/myapp:latest
```

## Mirror from Docker Hub on release

Keep a local copy of upstream images for faster pulls inside AWS:

```yaml
name: Mirror Images

on:
  schedule:
    - cron: '0 3 * * 1'  # every Monday at 3 AM
  workflow_dispatch:

jobs:
  mirror:
    runs-on: ubuntu-latest
    permissions:
      id-token: write
      contents: read

    steps:
      - uses: OuFinx/s3lo-action@v1
        with:
          role-to-assume: arn:aws:iam::123456789012:role/ci-s3lo-role
          aws-region: us-east-1

      - name: Mirror base images
        run: |
          s3lo copy alpine:3.19 s3://my-bucket/alpine:3.19
          s3lo copy nginx:1.25 s3://my-bucket/nginx:1.25
          s3lo copy python:3.11-slim s3://my-bucket/python:3.11-slim
```

## Scheduled cleanup { #scheduled-cleanup }

Run nightly to prune old tags and reclaim blob storage:

```yaml
name: Cleanup

on:
  schedule:
    - cron: '0 2 * * *'  # nightly at 2 AM UTC

jobs:
  clean:
    runs-on: ubuntu-latest
    permissions:
      id-token: write
      contents: read

    steps:
      - uses: OuFinx/s3lo-action@v1
        with:
          role-to-assume: arn:aws:iam::123456789012:role/ci-s3lo-role
          aws-region: us-east-1

      - name: Clean old tags and blobs
        run: s3lo clean s3://my-bucket/ --confirm
```

!!! tip
    Configure lifecycle rules before enabling this workflow:
    ```bash
    s3lo config set s3://my-bucket/ lifecycle.keep_last=10 lifecycle.max_age=90d
    ```

## Vulnerability scanning

Scan an image for vulnerabilities after push. Trivy is auto-installed on first run via `--install-trivy`:

```yaml
name: Build, Push, Scan

on:
  push:
    branches: [main]

jobs:
  build:
    runs-on: ubuntu-latest
    permissions:
      id-token: write
      contents: read

    steps:
      - uses: actions/checkout@v4

      - uses: OuFinx/s3lo-action@v1
        with:
          role-to-assume: arn:aws:iam::123456789012:role/ci-s3lo-role
          aws-region: us-east-1

      - name: Build and push
        run: |
          docker build --platform linux/amd64 -t myapp:${{ github.sha }} .
          s3lo push myapp:${{ github.sha }} s3://my-bucket/myapp:${{ github.sha }}

      - name: Scan for vulnerabilities
        run: |
          s3lo scan s3://my-bucket/myapp:${{ github.sha }} \
            --severity HIGH,CRITICAL \
            --install-trivy
```

The step exits non-zero if `HIGH` or `CRITICAL` vulnerabilities are found, failing the workflow. Remove `--severity` to report all severities without failing.

To upload results to the GitHub Security tab, use `--format sarif`:

```yaml
      - name: Scan (SARIF)
        run: |
          s3lo scan s3://my-bucket/myapp:${{ github.sha }} \
            --format sarif \
            --install-trivy > trivy-results.sarif

      - name: Upload SARIF to GitHub Security
        uses: github/codeql-action/upload-sarif@v3
        with:
          sarif_file: trivy-results.sarif
```

## Pull in deployment workflows

```yaml
- uses: OuFinx/s3lo-action@v1
  with:
    role-to-assume: arn:aws:iam::123456789012:role/ci-s3lo-role
    aws-region: us-east-1

- name: Pull image
  run: s3lo pull s3://my-bucket/myapp:${{ github.sha }}

- name: Deploy
  run: docker run -d myapp:${{ github.sha }}
```

## Without the action (manual install)

If you prefer not to use the action:

```yaml
- name: Install s3lo
  run: |
    curl -Lo s3lo.tar.gz https://github.com/OuFinx/s3lo/releases/latest/download/s3lo_linux_amd64.tar.gz
    tar xzf s3lo.tar.gz && chmod +x s3lo && sudo mv s3lo /usr/local/bin/
```

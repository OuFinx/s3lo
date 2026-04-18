# Getting Started

Get s3lo running and push your first image in under 5 minutes.

## Prerequisites

- Docker running locally
- For S3 storage: AWS account with an S3 bucket and credentials configured (`~/.aws/credentials`, environment variables, or IAM role)
- For GCS storage: Google Cloud project with a bucket, credentials via `gcloud auth application-default login` or `GOOGLE_APPLICATION_CREDENTIALS`
- For Azure Blob storage: Azure storage account, `AZURE_STORAGE_ACCOUNT` env var set, credentials via `az login` or service principal env vars
- For MinIO / R2 / Ceph: `AWS_ACCESS_KEY_ID` + `AWS_SECRET_ACCESS_KEY` + `--endpoint` flag
- For local storage: no cloud account needed — s3lo can use a local directory

## 1. Install s3lo

=== "macOS / Linux (recommended)"

    ```bash
    curl -sSL https://raw.githubusercontent.com/OuFinx/s3lo/main/install.sh | sh
    ```

=== "Homebrew"

    ```bash
    brew install OuFinx/tap/s3lo
    ```

=== "Manual"

    Download the binary for your platform from the [latest release](https://github.com/OuFinx/s3lo/releases/latest):

    ```bash
    # macOS (Apple Silicon)
    curl -Lo s3lo.tar.gz https://github.com/OuFinx/s3lo/releases/latest/download/s3lo_darwin_arm64.tar.gz
    tar xzf s3lo.tar.gz && sudo mv s3lo /usr/local/bin/

    # Linux (amd64)
    curl -Lo s3lo.tar.gz https://github.com/OuFinx/s3lo/releases/latest/download/s3lo_linux_amd64.tar.gz
    tar xzf s3lo.tar.gz && sudo mv s3lo /usr/local/bin/
    ```

=== "Go"

    ```bash
    go install github.com/OuFinx/s3lo/cmd/s3lo@latest
    ```

Verify the install:

```bash
s3lo version
# s3lo v1.5.0 (abc1234)
```

## 2. Create an S3 bucket

If you don't have one yet:

```bash
aws s3 mb s3://my-bucket --region us-east-1
```

!!! tip "Bucket settings"
    - **Versioning:** disable it — s3lo manages its own layout, versioning adds cost with no benefit
    - **Region:** pick the same region as your EC2 instances for maximum pull speed
    - **Access:** keep the default (private). s3lo uses your AWS credentials.

## 3. Set up IAM permissions

Your AWS credentials need access to the bucket. The minimum policy for push + pull:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "s3:GetObject",
        "s3:PutObject",
        "s3:HeadObject",
        "s3:ListBucket",
        "s3:GetBucketLocation"
      ],
      "Resource": [
        "arn:aws:s3:::my-bucket",
        "arn:aws:s3:::my-bucket/*"
      ]
    }
  ]
}
```

See [IAM Policies](reference/iam-policies.md) for per-command breakdowns and read-only variants.

## 4. Push your first image

```bash
s3lo push myapp:v1.0 s3://my-bucket/myapp:v1.0
```

Output:
```
Pushing myapp:v1.0 to s3://my-bucket/myapp:v1.0
  uploading ⠸ 58.7 MB [45s]
Done.
```

The first push uploads all blobs. Push again and nothing uploads (all blobs already exist):

```bash
s3lo push myapp:v1.0 s3://my-bucket/myapp:v1.0
# uploading ⠸ 0 B [0s]
# Done.
```

## 5. List and inspect

```bash
s3lo list s3://my-bucket/
# myapp:v1.0

s3lo inspect s3://my-bucket/myapp:v1.0
# Reference: s3://my-bucket/myapp:v1.0
# Type:      single-arch image
# Layers:    4
# Total:     58.70 MB
```

## 6. Pull on another machine

```bash
s3lo pull s3://my-bucket/myapp:v1.0
# Pulling s3://my-bucket/myapp:v1.0
#   downloading ⠸ 58.7 MB [30s]
# Done. Image imported into Docker.

docker run --rm myapp:v1.0
```

## Try without AWS: local storage

You can test s3lo without an AWS account using local storage:

```bash
# Initialize a local storage directory
s3lo init --local ./local-s3

# Push, pull, and list — same commands, just use local://
s3lo push myapp:v1.0 local://./local-s3/myapp:v1.0
s3lo pull local://./local-s3/myapp:v1.0
s3lo list local://./local-s3/
s3lo history local://./local-s3/
```

Local storage uses the same OCI layout as S3 storage — it's a great way to learn s3lo or run it in development.

## 7. Browse interactively

```bash
s3lo tui s3://my-bucket/
```

The TUI shows all images and tags with sizes, deduplication stats, and cost estimates. From inside a tag list, press `i` to inspect metadata, `s` to scan for vulnerabilities, `g` to view the layer sharing matrix, or `d` to delete. Press `q` to quit.

---

## Next steps

- **CI/CD:** [GitHub Actions integration](guides/github-actions.md) — push on every commit with OIDC auth
- **Mirror from Docker Hub / ECR:** use [`s3lo copy`](commands/copy.md) to pull from any registry directly to S3
- **Save money:** [configure lifecycle rules](guides/lifecycle.md) to automatically clean old tags
- **Browse interactively:** [`s3lo tui`](commands/tui.md) — full interactive UI for managing images
- **Understand the internals:** [How It Works](concepts/how-it-works.md)

# Authentication

s3lo uses the standard AWS SDK credential chain. It does not have its own authentication mechanism — any valid AWS credentials work.

## Credential resolution order

Credentials are resolved in this order:

1. `AWS_ACCESS_KEY_ID` + `AWS_SECRET_ACCESS_KEY` environment variables
2. `AWS_PROFILE` environment variable (selects a named profile)
3. `~/.aws/credentials` and `~/.aws/config` (default profile)
4. IAM instance profile (EC2, ECS task role, Lambda)
5. EKS Pod Identity / IRSA (IAM Roles for Service Accounts)
6. OIDC web identity token (GitHub Actions, GitLab CI)

## Region auto-detection

s3lo auto-detects the bucket region using `s3:GetBucketLocation`. You do not need to set `AWS_REGION` or pass a region flag.

## Common setups

=== "Local development"

    If you have AWS CLI configured, s3lo picks up the same credentials:

    ```bash
    # Uses default profile
    s3lo push myapp:v1.0 s3://my-bucket/myapp:v1.0

    # Use a named profile
    AWS_PROFILE=prod s3lo push myapp:v1.0 s3://my-bucket/myapp:v1.0
    ```

=== "AWS SSO"

    ```bash
    aws sso login --profile prod
    AWS_PROFILE=prod s3lo push myapp:v1.0 s3://my-bucket/myapp:v1.0
    ```

=== "EC2 / ECS"

    No configuration needed — s3lo picks up the instance/task IAM role automatically.

=== "GitHub Actions (OIDC)"

    The recommended approach — no long-lived credentials needed:

    ```yaml
    permissions:
      id-token: write
      contents: read

    steps:
      - uses: OuFinx/s3lo-action@v1
        with:
          role-to-assume: arn:aws:iam::123456789012:role/my-role
          aws-region: us-east-1

      - run: s3lo push myapp:${{ github.sha }} s3://my-bucket/myapp:${{ github.sha }}
    ```

    See the [GitHub Actions guide](../guides/github-actions.md) for full examples.

=== "Environment variables"

    ```bash
    AWS_ACCESS_KEY_ID=AKIA... \
    AWS_SECRET_ACCESS_KEY=... \
    s3lo push myapp:v1.0 s3://my-bucket/myapp:v1.0
    ```

## ECR sources

When using `s3lo copy` with an ECR source, s3lo calls `ecr:GetAuthorizationToken` automatically using the same credential chain. No separate ECR login step is required.

```bash
# Auto-authenticates to ECR using your AWS credentials
s3lo copy 123456789.dkr.ecr.us-east-1.amazonaws.com/myapp:v1.0 s3://my-bucket/myapp:v1.0
```

## No AWS CLI required

s3lo uses the AWS SDK directly. You don't need the AWS CLI installed — only valid credentials.

---

## Google Cloud Storage (gs://)

s3lo uses [Application Default Credentials (ADC)](https://cloud.google.com/docs/authentication/application-default-credentials) for GCS — the same mechanism used by the Google Cloud SDK and client libraries.

### Credential resolution order

1. `GOOGLE_APPLICATION_CREDENTIALS` environment variable (path to a service account JSON key file)
2. gcloud CLI credentials (`gcloud auth application-default login`)
3. Attached service account (Compute Engine, GKE, Cloud Run, etc.)

### Common setups

=== "Local development"

    ```bash
    # Authenticate with your Google account
    gcloud auth application-default login

    # Or use a service account key file
    export GOOGLE_APPLICATION_CREDENTIALS=/path/to/key.json

    s3lo push myapp:v1.0 gs://my-gcs-bucket/myapp:v1.0
    ```

=== "GKE Workload Identity"

    No configuration needed — s3lo picks up the attached service account automatically.

    Ensure the service account has the `Storage Object Admin` role (or a custom role with
    `storage.objects.create`, `storage.objects.get`, `storage.objects.list`, `storage.buckets.get`).

=== "Service account key (CI)"

    ```bash
    export GOOGLE_APPLICATION_CREDENTIALS=/path/to/sa-key.json
    s3lo push myapp:v1.0 gs://my-gcs-bucket/myapp:v1.0
    ```

### Required GCS permissions

The service account or user needs the following IAM roles on the bucket:

- `roles/storage.objectAdmin` — full read/write access
- For read-only (pull only): `roles/storage.objectViewer`

---

## Azure Blob Storage (az://)

s3lo uses [DefaultAzureCredential](https://learn.microsoft.com/en-us/azure/developer/go/azure-sdk-authentication) from the Azure SDK, which tries multiple auth mechanisms in order.

### Credential resolution order

1. `AZURE_CLIENT_ID` + `AZURE_CLIENT_SECRET` + `AZURE_TENANT_ID` environment variables (service principal)
2. `AZURE_CLIENT_ID` + `AZURE_FEDERATED_TOKEN_FILE` (workload identity / OIDC)
3. Azure CLI credentials (`az login`)
4. Managed Identity (Azure VMs, AKS, App Service, etc.)

### Storage account configuration

The storage account name must be set via the `AZURE_STORAGE_ACCOUNT` environment variable:

```bash
export AZURE_STORAGE_ACCOUNT=mystorageaccount
s3lo push myapp:v1.0 az://my-container/myapp:v1.0
```

The `az://` scheme uses the container name as the bucket equivalent. The storage account
is resolved from `AZURE_STORAGE_ACCOUNT`.

### Common setups

=== "Local development"

    ```bash
    # Authenticate with your Azure account
    az login

    export AZURE_STORAGE_ACCOUNT=mystorageaccount
    s3lo push myapp:v1.0 az://my-container/myapp:v1.0
    ```

=== "Service principal (CI)"

    ```bash
    export AZURE_TENANT_ID=...
    export AZURE_CLIENT_ID=...
    export AZURE_CLIENT_SECRET=...
    export AZURE_STORAGE_ACCOUNT=mystorageaccount
    s3lo push myapp:v1.0 az://my-container/myapp:v1.0
    ```

=== "AKS Workload Identity"

    No configuration needed beyond the pod's assigned managed identity. Set
    `AZURE_STORAGE_ACCOUNT` in the pod environment.

### Required Azure permissions

Assign the `Storage Blob Data Contributor` role to your identity on the storage account or container.
For read-only (pull only): `Storage Blob Data Reader`.

---

## S3-compatible (MinIO, R2, Ceph)

S3-compatible backends use the same AWS credentials chain with an added `--endpoint` flag.
No special authentication mechanism is required — set `AWS_ACCESS_KEY_ID` and
`AWS_SECRET_ACCESS_KEY` to the credentials for the compatible service.

```bash
# MinIO
AWS_ACCESS_KEY_ID=minioadmin AWS_SECRET_ACCESS_KEY=minioadmin \
  s3lo push myapp:v1.0 s3://my-bucket/myapp:v1.0 --endpoint http://localhost:9000

# Cloudflare R2
AWS_ACCESS_KEY_ID=<r2-key-id> AWS_SECRET_ACCESS_KEY=<r2-secret> \
  s3lo push myapp:v1.0 s3://my-bucket/myapp:v1.0 \
  --endpoint https://my-account.r2.cloudflarestorage.com
```

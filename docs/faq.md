# FAQ

## General

**Does s3lo support multi-architecture images?**

Yes, since v1.3.0. `copy` copies all platforms by default and preserves the full OCI Image Index. `pull` auto-detects the host platform — no flags needed for most workflows. See [Multi-Architecture](concepts/multi-arch.md).

**Can I use s3lo without Docker?**

Not currently. `push` and `pull` use the local Docker daemon (`docker save` / `docker load`). `copy` does not require Docker — it communicates directly with OCI registries and S3.

**Can I use s3lo without the AWS CLI?**

Yes. s3lo uses the AWS SDK directly and does not require the AWS CLI. You only need valid AWS credentials.

**Does `docker pull` work with s3lo images?**

No — S3 is not an OCI registry. You must use `s3lo pull`. For Kubernetes, consider [s3lo-operator](https://github.com/OuFinx/s3lo-operator), which acts as a pull-through proxy so you can use standard image references.

---

## Push and pull

**What happens if a push is interrupted?**

Blobs uploaded before the interruption stay in S3. Re-running the push will skip those blobs and continue. Manifest files are uploaded last — if interrupted before the manifest is written, the tag won't appear in `s3lo list`. Orphaned blobs are cleaned up by `s3lo clean --blobs`.

**Can two pushes run in parallel to the same tag?**

Blob uploads are safe (same content, same key). However, two simultaneous pushes of different images to the same tag create a race — the last manifest written wins. Serialize pushes to the same tag.

**Why does s3lo require an explicit tag?**

Commands like `pull`, `push`, `delete`, `inspect`, `scan`, and `copy` require an explicit tag (e.g. `s3://my-bucket/myapp:v1.0`). Writing `s3://my-bucket/myapp` without a tag is an error. This prevents accidental operations that silently default to `:latest`.

**Why does pull show "Done" but `docker images` shows nothing?**

Check that Docker is running. Also verify the image was pushed with the correct platform — on Apple Silicon, Docker produces `linux/arm64` images, which won't run on `linux/amd64` nodes. Use `--platform linux/amd64` when building or use `s3lo copy` to mirror a multi-arch image.

---

## Storage

**What is the maximum image size?**

No hard limit in s3lo. S3 supports objects up to 5 TB. s3lo uses single-part PutObject which supports up to 5 GB per blob. For images with individual blobs over 5 GB, multipart upload would be needed (not currently implemented).

**Does deduplication work across different image names?**

Yes — deduplication is bucket-wide. `myapp:v1.0` and `otherapp:latest` sharing the same base layer store it once, regardless of image name.

**How do I delete all tags for an image?**

```bash
s3lo list s3://my-bucket/ | grep "^myapp:" | while read ref; do
  s3lo delete "s3://my-bucket/$ref"
done
s3lo clean s3://my-bucket/ --blobs --confirm
```

**Does s3lo work with S3-compatible storage (MinIO, Cloudflare R2, Ceph, etc.)?**

Yes, since v1.10.0. Use the `--endpoint` flag to point s3lo at any S3-compatible server:

```bash
# MinIO
s3lo push myapp:v1.0 s3://my-bucket/myapp:v1.0 --endpoint http://localhost:9000

# Cloudflare R2
s3lo push myapp:v1.0 s3://my-bucket/myapp:v1.0 \
  --endpoint https://my-account.r2.cloudflarestorage.com
```

Set `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` to the credentials for the compatible service.

**Does s3lo support Google Cloud Storage?**

Yes, since v1.10.0. Use `gs://` references:

```bash
s3lo push myapp:v1.0 gs://my-gcs-bucket/myapp:v1.0
```

Authentication uses Application Default Credentials — run `gcloud auth application-default login` locally, or use a service account key via `GOOGLE_APPLICATION_CREDENTIALS`. See [Authentication](concepts/authentication.md#google-cloud-storage-gs).

**Does s3lo support Azure Blob Storage?**

Yes, since v1.10.0. Use `az://` references. Set `AZURE_STORAGE_ACCOUNT` to your storage account name:

```bash
export AZURE_STORAGE_ACCOUNT=mystorageaccount
s3lo push myapp:v1.0 az://my-container/myapp:v1.0
```

Authentication uses `DefaultAzureCredential` — `az login` locally, or service principal env vars in CI. See [Authentication](concepts/authentication.md#azure-blob-storage-az).

**Can I use s3lo without an AWS account?**

Yes. Use `s3lo init --local ./my-store` to create a local directory with the OCI layout, then use `local://` references (e.g. `local://./my-store/myapp:v1.0`). All commands — push, pull, list, history, inspect, delete, copy — work with local storage.

---

## Cost

**What does `s3lo copy` cost in terms of AWS charges?**

For ECR → S3 in the same region: ECR data transfer out is free within the same region. S3 PUT requests are $0.000005 each. For 100 blobs: ~$0.0005 total.

For S3 → S3 within the same bucket: uses S3 server-side `CopyObject` — no data transfer, no cost.

**Why is S3 cheaper than ECR?**

ECR charges $0.10/GB/month for storage. S3 Standard is $0.023/GB/month — about 4× cheaper. With deduplication and Intelligent-Tiering (which moves infrequently accessed layers to cheaper tiers automatically), the effective cost is even lower.

---

## Lifecycle

**How do lifecycle rules handle per-image overrides?**

Bucket-wide defaults apply to all images. Per-image overrides take precedence. When multiple glob patterns match an image, the most specific wins: exact matches before globs, longer patterns before shorter.

**What does the 1-hour GC grace period protect against?**

If a push is in progress and `clean --blobs` runs simultaneously, blobs that have been uploaded but whose manifest hasn't been written yet would be incorrectly identified as unreferenced and deleted. The grace period prevents this race condition.

---

## Vulnerability scanning

**Does s3lo include a vulnerability scanner?**

Yes, since v1.5.0. `s3lo scan` downloads an image from S3 and scans it with [Trivy](https://trivy.dev). Trivy is auto-installed to `~/.local/bin/trivy` on first use — s3lo will prompt you, or you can pass `--install-trivy` to skip the prompt in CI.

**Do I need to install Trivy separately?**

No. If Trivy isn't found in `PATH` or `~/.local/bin/`, s3lo offers to download it. Use `--install-trivy` in CI workflows to enable auto-install without a prompt.

**Can I fail a CI build when vulnerabilities are found?**

Yes. Pass `--severity HIGH,CRITICAL` (or any subset of `LOW,MEDIUM,HIGH,CRITICAL`). s3lo exits non-zero when Trivy finds vulnerabilities at or above the requested severity, which fails the workflow step.

**Does scanning require Docker?**

No. `s3lo scan` downloads the image blobs directly from S3 — Docker is not involved.

---

## Migration

**How do I migrate from ECR?**

Use `s3lo copy` — it authenticates automatically:

```bash
s3lo copy 123456789.dkr.ecr.us-east-1.amazonaws.com/myapp:v1.0 s3://my-bucket/myapp:v1.0
```

See the [ECR Migration guide](guides/ecr-migration.md) for bulk migration scripts.

# Commands

s3lo has a focused set of commands — one for each operation you'd do with a container registry, plus tools to manage storage.

| Command | What it does |
|---------|-------------|
| [`push`](push.md) | Export a local Docker image and upload it to S3 |
| [`pull`](pull.md) | Download an image from S3 and import it into Docker |
| [`serve`](serve.md) | Serve images via OCI Distribution Spec (docker pull compatible) |
| [`copy`](copy.md) | Copy an image to S3 from any OCI registry or another S3 bucket |
| [`list`](list.md) | List all images and tags in a bucket |
| [`inspect`](inspect.md) | Show image metadata: layers, size, platforms |
| [`delete`](delete.md) | Delete an image tag |
| [`clean`](clean.md) | Prune old tags and garbage collect unreferenced blobs |
| [`stats`](stats.md) | Show storage usage, deduplication savings, and cost estimate |
| [`config`](config.md) | Manage per-image and bucket-wide configuration |
| [`history`](history.md) | Show push history for a bucket or repository |
| [`scan`](scan.md) | Scan an image for vulnerabilities with Trivy |
| [`sbom`](sbom.md) | Generate a Software Bill of Materials (SBOM) for an image |
| [`sign`](sign.md) | Sign an image manifest with AWS KMS or a local key |
| [`verify`](verify.md) | Verify an image signature (exits 0/1/2 for CI gates) |

## Reference format

All commands that reference an image use the same format:

```
s3://bucket/image:tag
gs://bucket/image:tag
az://container/image:tag
local://path/image:tag
```

Examples:
```
s3://my-bucket/myapp:v1.0
s3://my-bucket/org/backend:latest
gs://my-gcs-bucket/myapp:v1.0
az://my-container/myapp:v1.0
local://./local-s3/alpine:latest
```

The scheme prefix (`s3://`, `gs://`, `az://`, or `local://`) is always required. Commands that operate on
a specific image (push, pull, delete, inspect, scan, copy) require an explicit tag —
omitting the tag is an error.

Bucket-level commands (list, history, stats, clean, config) accept a reference without a tag:

```
s3://my-bucket/
gs://my-gcs-bucket/
az://my-container/
local://./local-s3/
```

## S3-compatible backends

Use `--endpoint` to point s3lo at any S3-compatible storage such as MinIO, Cloudflare R2, or Ceph:

```bash
s3lo push myapp:v1.0 s3://my-bucket/myapp:v1.0 --endpoint http://localhost:9000
s3lo pull s3://my-bucket/myapp:v1.0 --endpoint https://my-account.r2.cloudflarestorage.com
```

The `--endpoint` flag is a persistent global flag available on all commands.

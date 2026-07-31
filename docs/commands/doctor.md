# doctor

Check that a bucket is reachable, holds an s3lo layout, and that every manifest's
blobs are present.

```
s3lo doctor <s3-bucket-ref> [--output json|yaml]
```

## Examples

```bash
s3lo doctor s3://my-bucket/
s3lo doctor local://./local-s3/
s3lo doctor s3://my-bucket/ --output json
```

## Output

```
Checking bucket: my-bucket

Checking access and credentials... OK
Checking layout structure...    OK
Checking config (s3lo.yaml)...  not configured (optional)
Checking manifest integrity...  OK
Checking for orphaned blobs...  3 blobs (12.4 MB)
  Note: clean skips blobs uploaded within the last hour (grace period).
Checking Intelligent-Tiering... not configured
  Note: enable S3 Intelligent-Tiering for automatic cost optimization on cold blobs.

  s3lo clean s3://my-bucket/ --blobs --confirm
```

## Checks

| Check | What it proves |
|-------|----------------|
| Access and credentials | The bucket exists and the current credentials can reach it. For `s3://` this is a `GetBucketLocation` call; for `local://` the directory must exist. |
| Layout structure | The bucket holds manifests or blobs — that is, it is an s3lo store and not an empty or wrong bucket. |
| Config | `s3lo.yaml` is absent (fine, it is optional) or present and readable. |
| Manifest integrity | Every blob a manifest references is stored, whole or as a chunk recipe. |
| Orphaned blobs | Blobs no manifest references, usually left by an interrupted push or a deleted tag. |
| Intelligent-Tiering | `s3://` only. An advisory, never a failure — S3 Intelligent-Tiering moves cold blobs to cheaper tiers automatically. |

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | Healthy. Orphaned blobs may still be reported — they cost storage but break nothing. |
| 1 | The bucket is unreachable, holds no s3lo layout, has an unreadable `s3lo.yaml`, or has corrupted manifests. |

This makes `doctor` usable as a CI gate:

```bash
s3lo doctor s3://my-bucket/ || exit 1
```

## Fixing what it finds

- **Orphaned blobs** — `s3lo clean s3://my-bucket/ --blobs --confirm`. Blobs
  uploaded in the last hour are skipped so a concurrent push is not sabotaged.
- **Corrupted manifests** — an image with missing blobs cannot be repaired.
  `doctor` prints the exact `s3lo delete` command for each one.
- **Unreachable bucket** — check the bucket name, the region, and that your
  credentials carry `s3:ListBucket` and `s3:GetBucketLocation`. See
  [IAM Policies](../reference/iam-policies.md).

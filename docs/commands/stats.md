# stats

Show storage usage, deduplication savings, and estimated monthly cost.

```
s3lo stats <s3-bucket-ref> [--layers] [--output json|yaml]
```

## Examples

```bash
s3lo stats s3://my-bucket/
s3lo stats local://./local-s3/
s3lo stats s3://my-bucket/ --layers
s3lo stats s3://my-bucket/ --layers --output json
```

## Output

```
Bucket: s3://my-bucket/

Images:       12
Tags:         47
Unique blobs: 89
Total size:   2.4 GB

Dedup savings: 1.8 GB (43% saved)

Storage class breakdown:
  INTELLIGENT_TIERING:           2.2 GB (91%)
  STANDARD:                      0.2 GB (9%)

Estimated monthly cost: $0.06
vs ECR equivalent:      $0.19 (3.2x cheaper)
```

## Layer sharing (`--layers`)

Lists every unique layer in the bucket, most-shared first, with the tags that reference it. Multi-arch tags are resolved to their platform manifests, so a layer shared by amd64 and arm64 is counted once.

```
Bucket: s3://my-bucket/

LAYER                  SIZE       TAGS  SHARED BY
sha256:aaaaaaaaaaaa    120.5 MB   3     myapp:v1.0, myapp:v1.1, worker:v2
sha256:bbbbbbbbbbbb    45.2 MB    2     myapp:v1.0, myapp:v1.1
sha256:cccccccccccc    8.1 MB     1     worker:v2

3 unique layers across 3 tags · 173.8 MB stored · 339.5 MB logical · 49% saved by sharing
```

## What it calculates

- **Logical size** — sum of all blobs referenced by all manifests (with duplicates counted multiple times, as a registry would charge)
- **Actual size** — real bytes stored in S3 (each unique blob counted once)
- **Dedup savings** — logical minus actual
- **Cost estimate** — based on S3 Intelligent-Tiering pricing ($0.023/GB/month for frequent access tier) for stored blobs + S3 Standard for manifests
- **ECR equivalent** — same logical size × ECR pricing ($0.10/GB/month)

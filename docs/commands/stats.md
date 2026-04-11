# stats

Show storage usage, deduplication savings, and estimated monthly cost.

```
s3lo stats <s3-bucket-ref>
```

## Example

```bash
s3lo stats s3://my-bucket/
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
vs ECR equivalent:      $0.24 (4.3x cheaper)
```

## What it calculates

- **Logical size** — sum of all blobs referenced by all manifests (with duplicates counted multiple times, as a registry would charge)
- **Actual size** — real bytes stored in S3 (each unique blob counted once)
- **Dedup savings** — logical minus actual
- **Cost estimate** — based on S3 Intelligent-Tiering pricing ($0.023/GB/month for frequent access tier) for stored blobs + S3 Standard for manifests
- **ECR equivalent** — same logical size × ECR pricing ($0.10/GB/month)

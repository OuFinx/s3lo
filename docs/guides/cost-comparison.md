# Cost Comparison

s3lo stores images in S3 at roughly 4–5× lower cost than ECR, with better deduplication and no per-transfer fees within AWS.

## Storage cost

| | ECR | S3 Standard | S3 Intelligent-Tiering |
|---|---|---|---|
| **Cost/GB/month** | $0.10 | $0.023 | $0.023 → $0.004 |
| **Deduplication** | None | n/a | Bucket-wide (SHA256) |
| **Auto-tiering** | No | No | Yes (after 30/90/180 days) |

s3lo stores blobs with S3 Intelligent-Tiering, which automatically moves objects to cheaper tiers as access frequency drops:

| Tier | Access pattern | Cost/GB/month |
|------|---------------|--------------|
| Frequent Access | Recently accessed | $0.023 |
| Infrequent Access | Not accessed for 30+ days | $0.0125 |
| Archive Instant Access | Not accessed for 90+ days | $0.004 |

Old images you keep for rollback (rarely pulled) move to Archive Instant Access automatically — no action needed.

## Data transfer cost

| Transfer type | ECR | S3 |
|--------------|-----|-----|
| Pull from EC2 (same region) | Free | Free |
| Pull from EC2 (cross-region) | $0.09/GB | $0.02/GB |
| Push from EC2 | Free | Free |
| Internet egress | $0.09/GB | $0.09/GB |

Inside AWS (EC2 → S3, same region), data transfer is free. s3lo's speed advantage comes from S3's throughput, not cost savings on transfer.

## Example: small team (5 images, 50 tags)

| | ECR | s3lo + S3 |
|---|---|---|
| Images | 5 | 5 |
| Tags | 50 | 50 |
| Logical size | 10 GB | 10 GB |
| Actual stored (with dedup) | 10 GB | ~3 GB |
| **Monthly storage cost** | **$1.00** | **~$0.07** |

## Example: larger team (30 images, 500 tags)

| | ECR | s3lo + S3 |
|---|---|---|
| Images | 30 | 30 |
| Tags | 500 | 500 |
| Logical size | 200 GB | 200 GB |
| Actual stored (with dedup, 60% savings) | 200 GB | ~80 GB |
| Intelligent-Tiering savings (50% of blobs in IA) | — | ~20% |
| **Monthly storage cost** | **$20.00** | **~$1.40** |

## Check your actual savings

```bash
s3lo stats s3://my-bucket/
```

```
Bucket: s3://my-bucket/

Images:       12
Tags:         47
Unique blobs: 89
Total size:   2.4 GB

Dedup savings: 1.8 GB (43% saved)

Estimated monthly cost: $0.06
vs ECR equivalent:      $0.24 (4.3x cheaper)
```

## Request costs

S3 charges per API request, but at the scale of container images this is negligible:

- PUT (push): $0.000005 per request → 100 blobs pushed = $0.0005
- GET (pull): $0.0000004 per request → 100 blobs pulled = $0.00004

ECR also charges per API request ($0.000010 per request) — similar or higher.

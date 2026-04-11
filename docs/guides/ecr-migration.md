# ECR Migration

Migrate your container images from ECR to S3 with a single command per image.

## Why migrate?

| | ECR | s3lo + S3 |
|---|---|---|
| Storage cost | $0.10/GB/month | $0.023/GB/month |
| Pull speed (EC2) | ~1–5 Gbps | Up to 100 Gbps |
| Auth | ECR token (expires in 12h) | Standard AWS credentials |
| Deduplication | None | Bucket-wide |
| Multi-region | Replicate per region | S3 Cross-Region Replication |

## Before you start

1. Create an S3 bucket in the same region as your ECR registry
2. Configure IAM permissions — your credentials need access to both ECR and S3:

```json
{
  "Effect": "Allow",
  "Action": ["ecr:GetAuthorizationToken", "ecr:BatchGetImage", "ecr:GetDownloadUrlForLayer"],
  "Resource": "*"
}
```

Plus the standard [s3lo push/pull policy](../reference/iam-policies.md).

## Migrate a single image

```bash
s3lo copy 123456789.dkr.ecr.us-east-1.amazonaws.com/myapp:v1.0 s3://my-bucket/myapp:v1.0
```

s3lo handles ECR authentication automatically using your AWS credentials.

## Migrate all tags for an image

```bash
ECR_REGISTRY="123456789.dkr.ecr.us-east-1.amazonaws.com"
IMAGE="myapp"
S3_BUCKET="my-bucket"

# List all tags for the image
aws ecr list-images --repository-name $IMAGE --query 'imageIds[*].imageTag' --output text \
  | tr '\t' '\n' \
  | while read tag; do
      echo "Migrating $IMAGE:$tag..."
      s3lo copy "$ECR_REGISTRY/$IMAGE:$tag" "s3://$S3_BUCKET/$IMAGE:$tag"
    done
```

## Migrate all repositories

```bash
ECR_REGISTRY="123456789.dkr.ecr.us-east-1.amazonaws.com"
S3_BUCKET="my-bucket"

aws ecr describe-repositories --query 'repositories[*].repositoryName' --output text \
  | tr '\t' '\n' \
  | while read repo; do
      aws ecr list-images --repository-name $repo --query 'imageIds[*].imageTag' --output text \
        | tr '\t' '\n' \
        | grep -v '^$' \
        | while read tag; do
            s3lo copy "$ECR_REGISTRY/$repo:$tag" "s3://$S3_BUCKET/$repo:$tag"
          done
    done
```

## Cutover strategy

Migrate without downtime by running both registries in parallel:

1. **Migrate images** — copy all existing tags from ECR to S3
2. **Update CI** — change push commands from ECR to s3lo
3. **Update deployments** — change pull commands from ECR to s3lo, one service at a time
4. **Verify** — confirm all services are pulling from S3 successfully
5. **Decommission** — once no traffic hits ECR, disable or delete the repositories

During the transition, push to both:

```bash
# Push to ECR (existing)
docker tag myapp:v1.0 $ECR_REGISTRY/myapp:v1.0
docker push $ECR_REGISTRY/myapp:v1.0

# Push to S3 (new)
s3lo push myapp:v1.0 s3://my-bucket/myapp:v1.0
```

## Cost during migration

`s3lo copy` uses server-side ECR → S3 transfer, which costs:

- **ECR data transfer out** — $0.09/GB (within the same region, this may be free or reduced)
- **S3 PUT requests** — negligible
- **S3 storage** — $0.023/GB/month going forward

The migration cost is a one-time expense, after which you pay only for S3 storage.

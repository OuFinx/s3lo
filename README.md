# s3lo

Store and retrieve OCI container images on AWS S3.

## Why?

- **Faster pulls**: S3 throughput on EC2 reaches 100 Gbps — much faster than ECR
- **Cheaper storage**: S3 costs less than ECR for image storage
- **Simple**: No registry to manage, just an S3 bucket

## Install

```bash
go install github.com/finx/s3lo@latest
```

## Usage

```bash
# Push a local image to S3
s3lo push myapp:v1.0 s3://my-bucket/myapp:v1.0

# Pull from S3
s3lo pull s3://my-bucket/myapp:v1.0 ./output

# List images
s3lo list s3://my-bucket/

# Inspect image
s3lo inspect s3://my-bucket/myapp:v1.0
```

## Authentication

Uses standard AWS credentials chain. Set `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY`, use `~/.aws/credentials`, or IAM instance profiles.

### Minimum IAM Policy

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["s3:GetObject", "s3:PutObject", "s3:HeadObject", "s3:ListBucket", "s3:GetBucketLocation"],
      "Resource": ["arn:aws:s3:::YOUR-BUCKET", "arn:aws:s3:::YOUR-BUCKET/*"]
    }
  ]
}
```

## S3 Storage Layout

Images are stored in OCI Image Layout format:

```
s3://bucket/image/tag/
├── index.json
├── manifest.json
├── config.json
└── blobs/
    ├── sha256:abc...
    └── sha256:def...
```

## See Also

- [s3lo-operator](https://github.com/finx/s3lo-operator) — Kubernetes DaemonSet for pulling S3 images natively in containerd

## License

MIT

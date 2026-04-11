# IAM Policies

## Full access (push + pull + manage)

Grants all s3lo operations including delete, clean, and config.

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "s3:GetObject",
        "s3:PutObject",
        "s3:DeleteObject",
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

## Read-only (pull only)

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "s3:GetObject",
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

## ECR source (for `s3lo copy` from ECR)

Add to the full access policy above:

```json
{
  "Effect": "Allow",
  "Action": [
    "ecr:GetAuthorizationToken",
    "ecr:BatchGetImage",
    "ecr:GetDownloadUrlForLayer"
  ],
  "Resource": "*"
}
```

## Per-command breakdown

| Command | Required S3 actions |
|---------|---------------------|
| `push` | GetObject, PutObject, HeadObject, ListBucket, GetBucketLocation |
| `pull` | GetObject, HeadObject, ListBucket, GetBucketLocation |
| `copy` (S3 src) | GetObject, PutObject, HeadObject, ListBucket, GetBucketLocation |
| `copy` (ECR src) | Same as push + `ecr:GetAuthorizationToken` |
| `list` | ListBucket, GetBucketLocation |
| `inspect` | GetObject, GetBucketLocation |
| `delete` | DeleteObject, ListBucket, GetBucketLocation |
| `clean` | GetObject, DeleteObject, ListBucket, GetBucketLocation |
| `stats` | GetObject, ListBucket, GetBucketLocation |
| `config set/get` | GetObject, PutObject, GetBucketLocation |
| `config recommend` | GetBucketLocation, GetBucketVersioning, GetBucketLifecycleConfiguration, ListMultipartUploads |

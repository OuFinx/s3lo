# Commands

s3lo has a focused set of commands — one for each operation you'd do with a container registry, plus tools to manage storage.

| Command | What it does |
|---------|-------------|
| [`push`](push.md) | Export a local Docker image and upload it to S3 |
| [`pull`](pull.md) | Download an image from S3 and import it into Docker |
| [`copy`](copy.md) | Copy an image to S3 from any OCI registry or another S3 bucket |
| [`list`](list.md) | List all images and tags in a bucket |
| [`inspect`](inspect.md) | Show image metadata: layers, size, platforms |
| [`delete`](delete.md) | Delete an image tag |
| [`clean`](clean.md) | Prune old tags and garbage collect unreferenced blobs |
| [`stats`](stats.md) | Show storage usage, deduplication savings, and cost estimate |
| [`config`](config.md) | Manage per-image and bucket-wide configuration |

## S3 reference format

All commands that reference an image on S3 use the same format:

```
s3://bucket/image:tag
s3://bucket/org/image:tag
```

Examples:
```
s3://my-bucket/myapp:v1.0
s3://my-bucket/org/backend:latest
s3://my-bucket/myapp:sha-abc1234
```

The bucket prefix (`s3://`) is always required.

# delete

Remove an image tag from S3.

```
s3lo delete <s3-ref>
```

The reference must include an explicit tag (e.g. `s3://my-bucket/myapp:v1.0`). Both `s3://` and `local://` references are supported.

## What it does

Deletes all files under `manifests/<image>/<tag>/`. Blobs are **not** touched — they remain in `blobs/sha256/` and may still be referenced by other tags.

To reclaim blob storage after deleting tags, run [`s3lo clean --blobs`](clean.md).

## Examples

```bash
# Delete a single tag (S3)
s3lo delete s3://my-bucket/myapp:v1.0

# Delete a tag from local storage
s3lo delete local://./local-s3/myapp:v1.0

# Delete a tag and immediately GC orphaned blobs
s3lo delete s3://my-bucket/myapp:v1.0
s3lo clean s3://my-bucket/ --blobs --confirm

# Delete all tags for an image
s3lo list s3://my-bucket/ | grep "^myapp:" | while read ref; do
  s3lo delete "s3://my-bucket/$ref"
done
s3lo clean s3://my-bucket/ --blobs --confirm
```

!!! warning
    Deleting a tag is immediate and irreversible. Blobs are safe until you run `clean --blobs`.

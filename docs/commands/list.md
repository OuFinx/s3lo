# list

List all images and tags stored in an S3 bucket or local storage.

```
s3lo list <bucket-ref>
```

## Examples

```bash
# List all images (S3)
s3lo list s3://my-bucket/

# List all images (local storage)
s3lo list local://./local-s3/

# Filter with grep
s3lo list s3://my-bucket/ | grep myapp

# Count total tags
s3lo list s3://my-bucket/ | wc -l
```

## Output

```
myapp:v1.0
myapp:v2.0
myapp:latest
api/backend:sha-abc1234
org/frontend:v3.1
```

Each line is a valid S3 reference — prefix with `s3://my-bucket/` to use with other commands:

```bash
# Delete all tags for myapp
s3lo list s3://my-bucket/ | grep "^myapp:" | while read ref; do
  s3lo delete "s3://my-bucket/$ref"
done
```

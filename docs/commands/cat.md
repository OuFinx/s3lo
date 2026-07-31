# cat

Read one file out of an image without pulling it.

```
s3lo cat <s3-ref> <path>
```

The reference must include an explicit tag. Both `s3://` and `local://`
references are supported.

## What it does

Resolves the path the way a running container would — layers from the top down,
whiteouts honoured, symlinks followed — and returns that file's bytes on stdout.

On a chunked bucket it fetches only the chunks the file's bytes fall in. A
registry cannot do this: the OCI Distribution Spec addresses whole layers, so the
same read there means downloading the layer that holds it.

```bash
$ s3lo cat s3://my-bucket/myapp:v1.0 /etc/os-release -o os-release
Wrote os-release (286 bytes, fetched 1 of 24 chunks)
```

That layer is 141 MB. The read moved one chunk.

## Examples

```bash
# Straight to stdout
s3lo cat s3://my-bucket/myapp:v1.0 /etc/os-release

# To a file — the summary goes to stderr, so stdout stays clean either way
s3lo cat s3://my-bucket/myapp:v1.0 /app/config.yaml -o config.yaml

# Pick a platform out of a multi-arch image
s3lo cat s3://my-bucket/myapp:v1.0 /etc/passwd --platform linux/arm64

# Compose with anything
s3lo cat s3://my-bucket/myapp:v1.0 /app/requirements.txt | grep torch
```

## Flags

| Flag | Description |
|---|---|
| `-o`, `--output` | Write to a file instead of stdout |
| `--platform` | Platform to read from a multi-arch image (e.g. `linux/arm64`) |

## When it is not cheap

A layer only has a file index if it was pushed as chunks. Layers stored whole —
either because the bucket has `chunked=false`, or because the layer is smaller
than one chunk, or because it was pushed before indexes existed — are still read
correctly, but the whole layer comes down to answer the question. The summary
line says which happened.

Re-pushing the image builds the index.

## Limits

A file removed by an opaque directory marker (`.wh..wh..opq`) rather than a plain
whiteout still reads. Plain whiteouts, which is what `RUN rm` produces, are
handled.

## See also

- [Chunked storage](../concepts/chunking.md) — what the index is built on
- [inspect](inspect.md) — image metadata rather than contents

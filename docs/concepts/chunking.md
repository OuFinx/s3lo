# Chunked storage

A container registry deduplicates whole layers. Change one file inside a 2 GB
layer and the layer gets a new digest, so the registry stores and transfers all
2 GB again. That is not a registry being wasteful — the OCI Distribution Spec
addresses layers, and a layer is the smallest thing it can talk about.

s3lo is not a registry and is not bound by that. It can split a layer and address
the pieces.

## Turning it on

Chunking is a property of the bucket, not of an image:

```bash
s3lo config set s3://my-bucket/ chunked=true
```

Every push after that stores layers as chunks. The chunk store is shared across
every image in the bucket, so images built on the same base share chunks even
when their layers differ.

It is safe to switch on or off at any time. Reads resolve a layer through its
recipe when one exists and fall back to a whole-layer blob when it does not, so
a bucket can hold both forms with no migration step.

## What it does to a re-push

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="../assets/bench-dedup-dark.svg">
  <img alt="Re-push uploads 4.1 MB regardless of layer size" src="../assets/bench-dedup-light.svg">
</picture>

Measured on a c6id.xlarge against real S3, rebuilding an image after editing one
file inside it:

| Layer being re-pushed | Uploaded | Chunks re-sent | Deduplicated |
|---|---|---|---|
| 131 MB | 4.1 MB | 1 of 36 | 96.9% |
| 512 MB | 4.1 MB | 1 of 127 | 99.2% |
| 1018 MB | 4.1 MB | 1 of 247 | 99.6% |
| 1673 MB | 4.1 MB | 1 of 354 | 99.8% |

The uploaded amount does not grow with the image. An edit costs one chunk,
whatever the layer weighs, which is why the percentage climbs rather than the
megabytes.

## How the boundaries are chosen

Chunks are cut by content, not at fixed offsets, using FastCDC with normalized
chunking. A rolling hash over the preceding bytes decides where a chunk ends, so
inserting or removing bytes shifts only the chunks around the edit — everything
before and after keeps its old boundaries and stays deduplicated. Fixed-size
blocks would shift every boundary after an insertion and deduplicate nothing.

Chunks average 4 MB, with a 1 MB floor and a 16 MB ceiling. Layers smaller than
one chunk are stored whole: splitting them would add a recipe object and an
indirection for no benefit.

## What lands in the bucket

```
chunks/sha256/<digest>     one object per unique chunk, zstd-compressed,
                           shared by every image in the bucket
recipes/sha256/<digest>    the ordered chunk list that rebuilds one layer
manifests/<image>/<tag>/   unchanged
```

A recipe is keyed by the layer's *compressed* digest, which is what the image
manifest references.

## Two digests, one layer

A chunked layer has two identities, and both matter:

- **The raw digest** is the sha256 of the uncompressed tar. It is the `diff_id`
  in the image config and it never changes. Because the config is untouched, the
  image ID stays the same as before chunking.
- **The compressed digest** is the sha256 of the chunk objects concatenated in
  order. The image manifest points at this one, with media type
  `application/vnd.oci.image.layer.v1.tar+zstd`.

That second identity works because zstd frames concatenate: joining the stored
chunks byte-for-byte produces a valid zstd stream. So `s3lo serve` hands a client
the chunk objects exactly as stored, decompressing nothing, and the client
decompresses as it would for any registry — moving roughly a third of the bytes
the raw layer would.

## Storage size

Chunks are stored zstd-compressed, so a chunked bucket is considerably smaller
than an unchunked one, which holds layers as raw tars. On `python:3.12-slim` the
same image occupies 142 MB unchunked and 37 MB chunked.

Compressing each chunk independently costs 0.4–1.5% of ratio compared with
compressing the whole layer as one stream — measured, and worth it for the
deduplication it buys.

## Format version

The first chunked push stamps the bucket with `chunk_format` in `s3lo.yaml`.

Chunk boundaries depend on the chunker's parameters, so if those ever change,
chunks written by the two versions cannot match. Rather than silently storing a
second, non-overlapping copy of everything while reporting no deduplication, a
build whose format differs refuses to write:

```
ERROR bucket chunk format mismatch: bucket was chunked with format 2,
this build writes format 1; chunks from the two do not deduplicate against each other
```

## Garbage collection

A chunked layer is reachable as manifest → recipe → chunks. `s3lo bucket clean`
follows that chain, so chunks still referenced by any live recipe survive, and
chunks whose last recipe is gone are collected along with it.

## What to check

```bash
s3lo bucket stats s3://my-bucket/
```

reports chunk count and how much of the bucket is chunk storage, and each push
prints what it actually had to upload:

```
Chunked: uploaded 4.1 MB of 1673.0 MB (99.8% deduplicated, 1/354 chunks)
```

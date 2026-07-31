# Go Library

s3lo exposes its core packages for use in other Go programs.

```bash
go get github.com/OuFinx/s3lo/v3
```

## Packages

| Package | Description |
|---------|-------------|
| `github.com/OuFinx/s3lo/v3/pkg/image` | High-level operations: push, pull, copy, list, inspect, delete, GC, stats |
| `github.com/OuFinx/s3lo/v3/pkg/ref` | Parse `s3://`, `gs://`, `az://`, `local://` references |
| `github.com/OuFinx/s3lo/v3/pkg/storage` | Storage backend interface + AWS S3, GCS, Azure Blob, and local filesystem implementations |
| `github.com/OuFinx/s3lo/v3/pkg/oci` | OCI manifest and config types |

All functions accept `context.Context` as the first argument.

## Push

```go
import "github.com/OuFinx/s3lo/v3/pkg/image"

err := image.Push(ctx, "myapp:v1.0", "s3://my-bucket/myapp:v1.0", image.PushOptions{})
```

With progress callback:

```go
err := image.Push(ctx, "myapp:v1.0", "s3://my-bucket/myapp:v1.0", image.PushOptions{
    OnBlob: func(digest string, size int64, skipped bool) {
        if skipped {
            fmt.Printf("skip %s\n", digest[:12])
        } else {
            fmt.Printf("upload %s (%d bytes)\n", digest[:12], size)
        }
    },
})
```

## Pull

```go
err := image.Pull(ctx, "s3://my-bucket/myapp:v1.0", "myapp:v1.0", image.PullOptions{})
```

Pull a specific platform from a multi-arch image:

```go
err := image.Pull(ctx, "s3://my-bucket/alpine:latest", "", image.PullOptions{
    Platform: "linux/amd64",
})
```

## Copy

```go
result, err := image.Copy(ctx, "alpine:latest", "s3://my-bucket/alpine:latest", image.CopyOptions{})
fmt.Printf("copied %d blobs, skipped %d\n", result.BlobsCopied, result.BlobsSkipped)
```

Copy a specific platform:

```go
result, err := image.Copy(ctx, "alpine:latest", "s3://my-bucket/alpine:latest", image.CopyOptions{
    Platform: "linux/amd64",
})
```

## List

```go
images, err := image.List(ctx, "s3://my-bucket/")
for _, img := range images {
    fmt.Println(img) // "myapp:v1.0"
}
```

## Inspect

```go
info, err := image.Inspect(ctx, "s3://my-bucket/myapp:v1.0")
fmt.Printf("layers: %d, size: %d bytes\n", len(info.Layers), info.TotalSize)

if info.IsIndex {
    for _, p := range info.Platforms {
        fmt.Printf("platform: %s, layers: %d\n", p.Platform, len(p.Layers))
    }
}
```

## Delete

```go
err := image.Delete(ctx, "s3://my-bucket/myapp:v1.0")
```

## Garbage collect

```go
// Dry run
result, err := image.GC(ctx, "s3://my-bucket/", true)
fmt.Printf("would delete %d blobs (%d bytes)\n", result.Candidates, result.FreedBytes)

// Apply
result, err = image.GC(ctx, "s3://my-bucket/", false)
fmt.Printf("deleted %d blobs (%d bytes freed)\n", result.Deleted, result.FreedBytes)
```

## Stats

```go
stats, err := image.Stats(ctx, "s3://my-bucket/")
fmt.Printf("images: %d, tags: %d, size: %d bytes\n", stats.Images, stats.Tags, stats.ActualBytes)
fmt.Printf("dedup savings: %d bytes\n", stats.LogicalBytes-stats.ActualBytes)
```

## Parse a reference

```go
import "github.com/OuFinx/s3lo/v3/pkg/ref"

r, err := ref.Parse("s3://my-bucket/myapp:v1.0")
fmt.Println(r.Scheme)            // "s3"
fmt.Println(r.Bucket)            // "my-bucket"
fmt.Println(r.Image)             // "myapp"
fmt.Println(r.Tag)               // "v1.0"
fmt.Println(r.ManifestsPrefix()) // "manifests/myapp/v1.0/"
```

All schemes are supported:

```go
ref.Parse("gs://my-gcs-bucket/myapp:v1.0")    // GCS
ref.Parse("az://my-container/myapp:v1.0")     // Azure Blob
ref.Parse("local://./store/myapp:v1.0")       // local filesystem
```

## S3-compatible endpoints

To use MinIO, Cloudflare R2, or Ceph, pass the endpoint via context:

```go
import "github.com/OuFinx/s3lo/v3/pkg/storage"

ctx = storage.WithEndpoint(ctx, "http://localhost:9000")
err := image.Push(ctx, "myapp:v1.0", "s3://my-bucket/myapp:v1.0", image.PushOptions{})
```

## Chunked layers

`pkg/chunkstore` stores a layer as content-defined chunks and rebuilds it, in
either of the two forms a chunked layer has:

```go
import "github.com/OuFinx/s3lo/v3/pkg/chunkstore"

// Split a layer into chunks, uploading only the ones the bucket lacks.
recipe, stats, err := chunkstore.Store(ctx, client, "my-bucket", "/tmp/layer.tar", rawDigest)
fmt.Printf("uploaded %d of %d chunks (%.1f%% deduplicated)\n",
    stats.ChunksUploaded, stats.Chunks, stats.Deduplicated()*100)

// Rebuild the raw tar, verifying it against the layer digest.
err = chunkstore.Fetch(ctx, client, "my-bucket", recipe, "/tmp/out.tar")

// Or stream the stored chunks as-is: a valid zstd stream matching
// recipe.CompressedDigest, which is what an image manifest references.
err = chunkstore.StreamCompressed(ctx, client, "my-bucket", recipe, w)
```

`recipe.Layer` is the raw digest (the config's `diff_id`, unchanged by
chunking); `recipe.CompressedDigest` identifies the compressed stream.

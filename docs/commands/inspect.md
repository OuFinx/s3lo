# inspect

Show image metadata: layers, total size, platform details for multi-arch images, and stored signatures.

```
s3lo inspect <s3-ref>
```

The reference must include an explicit tag. Both `s3://` and `local://` references are supported.

## Examples

```bash
s3lo inspect s3://my-bucket/myapp:v1.0
s3lo inspect s3://my-bucket/alpine:latest
s3lo inspect local://./local-s3/alpine:latest
```

## Output

=== "Single-arch"

    ```
    Reference: s3://my-bucket/myapp:v1.0
    Type:      single-arch image
    Layers:    4
    Total:     58.70 MB

      [1] sha256:a1b2c3d4e5f67890...  (45.20 MB)
      [2] sha256:e5f6g7h8i9j01234...  (12.10 MB)
      [3] sha256:i9j0k1l2m3n45678...   (1.30 MB)
      [4] sha256:m3n4o5p6q7r89012...   (0.10 MB)
    ```

=== "Multi-arch"

    ```
    Reference: s3://my-bucket/alpine:latest
    Type:      multi-arch image index (7 platform(s))

      Platform: linux/amd64
      Digest:   sha256:a1b2c3d4e5f...
      Layers:   4
      Size:     7.80 MB

      Platform: linux/arm64
      Digest:   sha256:b2c3d4e5f6a...
      Layers:   4
      Size:     7.20 MB

      Platform: linux/arm/v7
      Digest:   sha256:c3d4e5f6a7b...
      Layers:   4
      Size:     6.90 MB
      ...

    Signatures:
      3b780f64bd6940e1  (key: awskms:///alias/release-signer, signed: 2026-04-12T19:21:10Z)
      21b44047667b31d3  (key: cosign.key, signed: 2026-04-12T19:21:21Z)
    ```

The `Signatures` section appears only when at least one signature is stored for the image.
Use [`s3lo sign`](sign.md) to sign an image and [`s3lo verify`](verify.md) to verify.

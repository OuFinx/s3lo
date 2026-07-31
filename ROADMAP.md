# s3lo Roadmap

## Shipping in v2

The command set as it stands:

| | |
|---|---|
| `push` `pull` `copy` `delete` | move images between Docker, registries and object storage |
| `list` `inspect` `stats` `cat` | read what is stored, including one file out of an image |
| `sign` `verify` | cosign signing and a CI-gateable verification exit code |
| `doctor` `clean` `config` | bucket health, retention, per-image settings |
| `serve` | OCI Distribution Spec endpoint, so `docker pull` works directly |

Backends: AWS S3, GCS, Azure Blob, local directories, and anything S3-compatible
(MinIO, Cloudflare R2, Ceph) via `--endpoint`.

Layers are stored as content-defined chunks shared across every image in the
bucket, which is what makes a re-push after editing one file cost one chunk
rather than a whole layer, and what makes `cat` able to read a single file
without downloading the layer holding it.

### Removed in v2

`scan`, `sbom`, `history`, `config validate`, `config recommend`, `migrate`, the
interactive TUI, and `bucket init`. Earlier versions of this file listed several
of these as shipped; they are gone. The `bucket` and `security` command groups
were flattened, and the old spellings still work for one release with a
deprecation notice.

The layer-sharing view that lived in the TUI is now `s3lo stats --layers`.
Bucket layout is created on first push, so there is nothing to initialise.

## Next

- [ ] Raise coverage on `pkg/storage` and `pkg/oci` — the code that actually
      moves bytes is the least tested in the repo.
- [ ] Stream blobs in `serve` rather than buffering them, so concurrent pulls of
      large layers do not scale memory with request count.

## Known limitations

Current behaviour, deliberately, rather than defects awaiting a fix.

**`push` publishes one platform.** It exports what the local Docker daemon
holds. Use `copy` for multi-architecture images — it handles a full OCI image
index and copies every platform by default.

**`copy` from a registry stores whole layers, not chunks.** Registry layers
arrive gzip-compressed, and chunking them would mean republishing each layer
under a new digest, changing the image's manifest digest. So an image onboarded
with `copy` does not deduplicate against pushed images, and `s3lo cat` cannot
read individual files out of it — `cat` reports that the layer is stored
compressed rather than claiming the file is absent.

If you want chunking and per-file reads for an image that lives in a registry,
pull it and `push` it.

**Immutability is advisory.** It is enforced by this client, not by the bucket:
anyone with `s3:PutObject` can rewrite `s3lo.yaml` to turn it off, and `--force`
bypasses it. For enforcement that survives a hostile client, use S3 Object Lock
or a bucket policy.

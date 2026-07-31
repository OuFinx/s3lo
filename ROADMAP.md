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

- [ ] `push` handles multi-architecture images ([#101](https://github.com/OuFinx/s3lo/issues/101)).
      `copy` already does; `push` exports only what the local daemon holds.
- [ ] `copy` from a registry stores chunked, so copied images deduplicate and
      support `cat` ([#102](https://github.com/OuFinx/s3lo/issues/102)). Today a
      bucket behaves one way for `push` and another for `copy`.
- [ ] Bucket-level commands honour a prefixed reference instead of silently
      scanning nothing ([#100](https://github.com/OuFinx/s3lo/issues/100)).
- [ ] `--output json` on the remaining commands
      ([#95](https://github.com/OuFinx/s3lo/issues/95)).
- [ ] Raise coverage on `pkg/storage` and `pkg/oci` — the code that actually
      moves bytes is the least tested in the repo.
- [ ] Stream blobs in `serve` rather than buffering them, so concurrent pulls of
      large layers do not scale memory with request count.

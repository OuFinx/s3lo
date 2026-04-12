# history

Show push history for a bucket or a specific repository.

```
s3lo history <ref> [flags]
```

## Modes

`history` works at two levels depending on the reference you pass:

| Reference | Mode | What it shows |
|-----------|------|---------------|
| `s3://bucket/` or `local://./store/` | Bucket | Summary of all repositories |
| `s3://bucket/myapp` or `local://./store/myapp` | Repository | All tags for that image |

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--limit` | `10` | Maximum number of rows to show (0 = all) |
| `--all` | `false` | Include overridden tag pushes (repository mode only) |
| `-o, --output` | table | Output format: `json`, `yaml`, or `table` |

## Bucket-level history

Shows one row per repository with the tag count, most recent push time, and aggregate size.

```bash
s3lo history s3://my-bucket/
s3lo history local://./local-s3/
```

```
REPOSITORY            TAGS   LAST PUSHED           TOTAL SIZE
------------------------------------------------------------------
myapp                 5      2026-04-12 14:30:00   120.8 MB
alpine                2      2026-04-12 14:14:12   3.5 MB
```

## Repository-level history

Shows the current (latest) push for each tag in the repository.

```bash
s3lo history s3://my-bucket/myapp
s3lo history local://./local-s3/alpine
```

```
TAG                   PUSHED                SIZE        DIGEST
--------------------------------------------------------------------------------
latest                2026-04-12 14:30:00   3.5 MB      sha256:1ab49c19c...
v3.18                 2026-04-10 09:15:00   3.4 MB      sha256:45f3ea584...
```

### Overridden pushes

Tags like `latest` can be pushed multiple times (each push overwrites the previous). By default, only the current version is shown. Use `--all` to see the full audit trail — older pushes are marked `(overridden)`:

```bash
s3lo history s3://my-bucket/alpine --all
```

```
TAG                   PUSHED                SIZE        DIGEST
--------------------------------------------------------------------------------
latest                2026-04-12 14:30:00   3.5 MB      sha256:1ab49c19c...
latest (overridden)   2026-04-11 09:00:00   3.5 MB      sha256:7d83bbf21...
v3.18                 2026-04-10 09:15:00   3.4 MB      sha256:45f3ea584...
```

In JSON/YAML output, overridden entries have `"superseded": true`.

## Examples

```bash
# All repositories in a bucket
s3lo history s3://my-bucket/

# All tags for one image
s3lo history s3://my-bucket/myapp

# Full audit trail including overridden pushes
s3lo history s3://my-bucket/myapp --all

# JSON output for scripting
s3lo history s3://my-bucket/ --output json

# Show only the 5 most recent
s3lo history s3://my-bucket/myapp --limit 5

# Local storage
s3lo history local://./local-s3/
s3lo history local://./local-s3/alpine
```

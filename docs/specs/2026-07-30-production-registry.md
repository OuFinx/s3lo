# s3lo as a production image store

Status: draft
Date: 2026-07-30

## Positioning

s3lo is not a registry and should stop apologising for it. It is an image store
that is **not bound by the OCI Distribution Spec**, and every advantage below
follows from that single fact. Registries address whole layers, must move a
layer's chunks sequentially, and must remain pullable by a standard client.
s3lo talks to object storage directly and has none of those constraints.

The goal of this document is to turn that from a curiosity into something a
team would put in production.

## The pains, as documented

Sourced from AWS docs and field reports, not from imagination.

| # | Pain | Evidence |
|---|---|---|
| 1 | **Auth token expires every 12 hours.** Non-EKS clusters, multi-account setups and ArgoCD need CronJobs or a dedicated operator purely to rotate `imagePullSecret`. An entire category of tooling exists for this. | ecr-secret-operator and the many "refresh ECR token" CronJob posts |
| 2 | **API throttling under scale-out.** `GetAuthorizationToken` is 20 TPS sustained (200 burst). Every API op has its own throttle. Users report `toomanyrequests: Rate exceeded` on push, and AWS's own docs note there is no indication of *which* call is being throttled. | [ECR common errors](https://docs.aws.amazon.com/AmazonECR/latest/userguide/common-errors.html), [awsdocs issue #11](https://github.com/awsdocs/amazon-ecr-user-guide/issues/11) |
| 3 | **NAT Gateway tax: $0.045/GB.** Private-subnet nodes pull through NAT by default. Avoiding it needs *three* VPC endpoints (`ecr.dkr`, `ecr.api`, and an S3 gateway); people routinely miss the S3 one and pulls fail even with the other two. | [ECR pricing analysis](https://cloudburn.io/blog/amazon-ecr-pricing) |
| 4 | **Cross-region pulls cost $0.09/GB**, and replication does not copy lifecycle policies, scan settings, or tag mutability. | [ECR replication docs](https://docs.aws.amazon.com/AmazonECR/latest/userguide/replication.html) |
| 5 | **Lifecycle policies are a footgun.** Rules evaluate top-down, first match wins, so a broad rule above a narrow one silently overrides it. Getting it wrong deletes images you meant to keep. | ECR pricing analysis, above |
| 6 | **Unbounded storage growth.** Without a lifecycle policy every CI build accumulates forever. | ECR pricing analysis |
| 7 | **Hard caps.** Max layer size 52,000 MiB; layer parts capped at 4,200 with a 10 MiB max part size. | [ECR service quotas](https://docs.aws.amazon.com/AmazonECR/latest/userguide/service-quotas.html) |
| 8 | **Layer-granularity dedup.** Change one file in a 2 GB layer and the whole layer is a new blob, stored and transferred in full. | OCI Distribution Spec |
| 9 | **Serial pull path.** containerd defaults to 3 concurrent layer downloads, one connection per layer, and **1** concurrent unpack per image. gzip cannot be decompressed in parallel at any setting. | [AWS SOCI parallel pull](https://aws.amazon.com/blogs/containers/introducing-seekable-oci-parallel-pull-mode-for-amazon-eks/) |
| 10 | **Repositories must exist before push**, with their own policies and settings to manage. | ECR docs |

## What s3lo answers, and how

| Pain | Answer | Status |
|---|---|---|
| 1 | No token at all. IAM/IRSA and bucket policy. Nothing to rotate, no CronJob, no operator for secrets. | already true |
| 2 | S3 gives 5,500 GET/s **per prefix** with unlimited prefixes. Chunk digests spread across prefixes by hash, so fan-out scales by construction. | strengthened by chunking |
| 3 | An S3 **gateway** endpoint is free and most VPCs already have one. One endpoint, no NAT, no $0.045/GB. | already true |
| 4 | S3 replication, or a single bucket, or another cloud entirely. | already true |
| 5 | s3lo's own lifecycle rules, dry-run by default. | already true |
| 6 | Chunk-level dedup plus lifecycle plus GC. | needs chunking + GC work |
| 7 | No layer size cap; parts are our own concern. | already true |
| 8 | **Content-defined chunking.** Measured: a 9-byte edit in a 64 MB layer re-uploads one 8 MB chunk instead of 64 MB. Cost is ~one chunk regardless of layer size. | implemented, not wired |
| 9 | Chunks decompress **in parallel** because each is its own zstd stream. This is the one thing gzip cannot do at any concurrency setting. | needs proxy work |
| 10 | A key prefix. Nothing to create. | already true |

Storage cost, measured on `python:3.12-slim`, per GB of raw image content:

| | stored as | $/GB/month |
|---|---|---|
| ECR | gzip, 3.4x | $0.0294 |
| s3lo today | uncompressed | $0.0230 |
| s3lo chunked | zstd, ~4x | **$0.0058** |

Per-chunk compression costs only 0.4-1.5% of ratio versus whole-stream, measured.

## The keystone

Today `s3lo-operator` answers a blob request with a `307` to a presigned S3 URL.
That is architecturally **identical to ECR**, which also hands out presigned S3
URLs, so the operator wins nothing structural.

Chunking changes this by force. A chunked layer does not exist as one object, so
the proxy must assemble it. Entering the data path is exactly what unlocks:

- parallel chunk fetch (S3 has no per-layer connection limit)
- parallel zstd decode across cores
- range-served assembly, so containerd starts receiving bytes immediately

The layer digest is unchanged, so containerd verifies what it receives exactly as
before. Nothing about the image changes; only how the bytes reach the node.

This is the piece that turns a CLI trick into a cluster-level advantage.

## Gaps that block production

Honest list. These are not optional.

1. **Tag writes are not atomic.** `Push` uploads `manifest.json`, `config.json`,
   `index.json` and `oci-layout` in a loop. A concurrent reader can observe a
   half-written tag, and a failed push leaves one behind. Needs a single
   immutable manifest object plus an atomic pointer swap.
2. **GC does not know about chunks.** `bucket clean` collects references by
   scanning manifests. With chunking the chain becomes
   manifest → recipe → chunks; without that, GC deletes every chunk as
   unreferenced. This is a data-loss bug the moment chunking ships.
3. **Push is not resumable.** A failed push of a large image restarts from zero.
4. **No multi-arch push.** `ExportImage` takes `entries[0]` from `docker save`.
   `copy` handles image indexes, `push` does not.
5. **Operator is immature** and, as noted, currently gains nothing.
6. **No metrics.** Nothing to alert on in production.

## Plan

Ordered so that nothing ships in a broken intermediate state.

1. **GC chunk-awareness** — before chunked push exists, so it can never run
   against data it does not understand.
2. **Atomic tag writes** — content-addressed manifest object plus pointer swap.
   Fixes a real correctness bug that exists today, independent of chunking.
3. **Wire chunking into push/pull** behind a bucket setting, with `bucket stats`
   reporting dedup.
4. **Measure on real images** on EC2 against real S3: push, re-push after a
   rebuild, pull, cold and warm.
5. **Operator: assemble chunks in the data path**, parallel fetch and decode.
6. **Metrics, resumable push, multi-arch push.**

## Not doing

Saying no is part of the design.

- Becoming an OCI Distribution endpoint. That would re-impose every constraint
  this design exists to escape.
- Vulnerability scanning, RBAC per repository, webhooks. Bucket policy is the
  access model; scanning belongs to tools that already do it well.
- Replacing ECR for teams whose pulls are already fast enough. If images are
  200 MB and nobody is complaining, there is nothing here worth a migration.

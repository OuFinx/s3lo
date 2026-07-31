#!/usr/bin/env bash
# Benchmark s3lo against ECR on one node, the way a pod start actually works:
# containerd pulling one image, cold.
#
# Run this ON the benchmark instance. It expects s3lo, crictl, docker, aws and a
# containerd configured with a hosts.toml for localhost:5000 (see the handoff
# notes for the instance setup).
#
# Usage:  BUCKET=my-bench-bucket ./benchmark.sh 100 500 1000 3000
#
# Three things make a container-image benchmark lie, and all three are guarded
# here because all three produced convincing wrong numbers during development:
#
#   1. `crictl rmi` only drops the image record. The blobs stay in the content
#      store until an asynchronous GC runs, so an immediate re-pull finds
#      everything present and "completes" in 0.2 s having transferred nothing.
#      The whole content store is wiped between runs instead.
#   2. A registry on localhost is invisible to the NIC byte counter, so the
#      loopback counter is read as well. A pull that moved nothing looks
#      identical to a fast one otherwise.
#   3. `$?` must be captured immediately after the pull. Reading it after any
#      other command reports that command's status, which silently turns failed
#      pulls into successful-looking zero-second ones.
set -euo pipefail

: "${BUCKET:?set BUCKET to the benchmark bucket name}"
ACCT=$(aws sts get-caller-identity --query Account --output text)
REGION=${AWS_REGION:-us-east-1}
ECR=$ACCT.dkr.ecr.$REGION.amazonaws.com
REPO=$ECR/s3lo-bench
IFACE=$(ip -o -4 route show to default | awk '{print $5}')
FAST=${FAST:-/mnt/fast}
RESULTS=${RESULTS:-$FAST/results.csv}
export TMPDIR=$FAST/tmp
mkdir -p "$TMPDIR" "$FAST/payload"

rx()  { cat "/sys/class/net/$IFACE/statistics/rx_bytes"; }
lo()  { cat /sys/class/net/lo/statistics/rx_bytes; }
now() { date +%s.%N; }

# Build a payload of roughly the requested size from the real corpus. Files above
# 64 MB are skipped so one huge file cannot blow past the budget.
build_payload() {
  local mb=$1 dir=$FAST/payload/p$1
  [ -d "$dir" ] && return 0
  mkdir -p "$dir"
  local budget=$((mb * 1024 * 1024)) used=0 n=0 sz
  while read -r f; do
    sz=$(stat -c %s "$f" 2>/dev/null) || continue
    [ "$sz" -eq 0 ] && continue
    [ "$sz" -gt 67108864 ] && continue
    [ $((used + sz)) -gt "$budget" ] && continue
    cp "$f" "$dir/f$(printf %06d $n).bin" 2>/dev/null || continue
    used=$((used + sz)); n=$((n + 1))
  done < "$FAST/filelist.txt"
  echo "payload ${mb}MB: $n files, $((used / 1048576)) MB actual"
}

# Wipe containerd's content store so the next pull is genuinely cold.
reset_containerd() {
  systemctl stop containerd
  rm -rf "$FAST/containerd/io.containerd.content.v1.content" \
         "$FAST/containerd/io.containerd.snapshotter.v1.overlayfs" \
         "$FAST/containerd/io.containerd.metadata.v1.bolt"
  systemctl start containerd
  for _ in $(seq 1 30); do crictl version >/dev/null 2>&1 && return; sleep 1; done
  echo "containerd did not come back" >&2; exit 1
}

# Prints: elapsed net_bytes lo_bytes store_delta_mb exit_code
cold_pull() {
  local ref=$1 creds=${2:-}
  reset_containerd
  sync; echo 3 > /proc/sys/vm/drop_caches
  local store0 r0 l0 t0 rc t1 r1 l1 store1
  store0=$(du -sm "$FAST/containerd/io.containerd.content.v1.content" 2>/dev/null | cut -f1 || echo 0)
  r0=$(rx); l0=$(lo); t0=$(now)
  if [ -n "$creds" ]; then
    crictl pull --creds "$creds" "$ref" >/tmp/pull.log 2>&1
  else
    crictl pull "$ref" >/tmp/pull.log 2>&1
  fi
  rc=$?                       # must be read here, before anything else runs
  t1=$(now); r1=$(rx); l1=$(lo)
  store1=$(du -sm "$FAST/containerd/io.containerd.content.v1.content" 2>/dev/null | cut -f1 || echo 0)
  echo "$(echo "$t1 - $t0" | bc) $((r1 - r0)) $((l1 - l0)) $((store1 - store0)) $rc"
}

run_size() {
  local MB=$1 TAG=s$1
  echo "===== ${MB}MB ====="
  build_payload "$MB"

  # Every size starts from an empty bucket so its first push is a real first push.
  aws s3 rm "s3://$BUCKET/" --recursive --only-show-errors
  s3lo config set "s3://$BUCKET/" chunked=true >/dev/null
  aws ecr batch-delete-image --repository-name s3lo-bench --image-ids imageTag="$TAG" >/dev/null 2>&1 || true

  cd "$FAST/payload"
  printf 'FROM public.ecr.aws/docker/library/alpine:3.20\nCOPY p%s /data\n' "$MB" > "Dockerfile.$MB"
  docker build -q -f "Dockerfile.$MB" -t "bench:$TAG" . >/dev/null

  aws ecr get-login-password --region "$REGION" | docker login --username AWS --password-stdin "$ECR" >/dev/null 2>&1
  docker tag "bench:$TAG" "$REPO:$TAG"
  local t0 t1 ecr_push s3lo_push ecr_stored s3lo_stored out
  t0=$(now); docker push -q "$REPO:$TAG" >/dev/null; t1=$(now)
  ecr_push=$(echo "$t1 - $t0" | bc)
  ecr_stored=$(aws ecr describe-images --repository-name s3lo-bench --image-ids imageTag="$TAG" \
    --query 'imageDetails[0].imageSizeInBytes' --output text)

  t0=$(now); out=$(s3lo push "bench:$TAG" "s3://$BUCKET/bench:$TAG" 2>&1); t1=$(now)
  s3lo_push=$(echo "$t1 - $t0" | bc)
  s3lo_stored=$(aws s3 ls "s3://$BUCKET/" --recursive --summarize | awk '/Total Size/{print $3}')
  echo "  s3lo first push: $(echo "$out" | grep Chunked || echo n/a)"

  # Re-push after a one-file edit: the deduplication claim.
  echo "changed-$(date +%s)" >> "$FAST/payload/p$MB/f000000.bin"
  docker build -q -f "Dockerfile.$MB" -t "bench:${TAG}b" . >/dev/null
  local out2 s3lo_repush ecr_repush
  t0=$(now); out2=$(s3lo push "bench:${TAG}b" "s3://$BUCKET/bench:${TAG}b" 2>&1); t1=$(now)
  s3lo_repush=$(echo "$t1 - $t0" | bc)
  echo "  s3lo re-push:    $(echo "$out2" | grep Chunked || echo n/a)"
  docker tag "bench:${TAG}b" "$REPO:${TAG}b"
  t0=$(now); docker push -q "$REPO:${TAG}b" >/dev/null; t1=$(now)
  ecr_repush=$(echo "$t1 - $t0" | bc)

  pkill -f "s3lo serve" 2>/dev/null || true; sleep 1
  nohup s3lo serve "s3://$BUCKET/" --port 5000 --host 127.0.0.1 > "$FAST/serve.log" 2>&1 &
  sleep 3
  local CREDS="AWS:$(aws ecr get-login-password --region "$REGION")"

  local i e net lo_b st rc
  for i in 1 2 3; do
    read -r e net lo_b st rc <<< "$(cold_pull "$REPO:$TAG" "$CREDS")"
    echo "$MB,ecr,pull,$i,$e,$net,$lo_b,$st,$rc" >> "$RESULTS"
    printf '    ecr  run%d: %ss net=%sMB store+=%sMB rc=%s\n' "$i" "$e" "$((net/1048576))" "$st" "$rc"
    read -r e net lo_b st rc <<< "$(cold_pull "localhost:5000/bench:$TAG" "")"
    echo "$MB,s3lo,pull,$i,$e,$net,$lo_b,$st,$rc" >> "$RESULTS"
    printf '    s3lo run%d: %ss net=%sMB lo=%sMB store+=%sMB rc=%s\n' \
      "$i" "$e" "$((net/1048576))" "$((lo_b/1048576))" "$st" "$rc"
  done
  pkill -f "s3lo serve" 2>/dev/null || true

  {
    echo "$MB,ecr,push,1,$ecr_push,$ecr_stored,0,0,0"
    echo "$MB,s3lo,push,1,$s3lo_push,$s3lo_stored,0,0,0"
    echo "$MB,ecr,repush,1,$ecr_repush,0,0,0,0"
    echo "$MB,s3lo,repush,1,$s3lo_repush,0,0,0,0"
  } >> "$RESULTS"
  echo "  ecr_push=${ecr_push}s stored=$ecr_stored | s3lo_push=${s3lo_push}s stored=$s3lo_stored"
}

echo "size,side,phase,run,seconds,net_bytes,lo_bytes,store_mb,rc" > "$RESULTS"
for mb in "$@"; do run_size "$mb"; done
echo
echo "results: $RESULTS"

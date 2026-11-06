#!/bin/bash
# Usage: pool-bench.sh OUTDIR "<n>:<p> ..."   e.g. pool-bench.sh /tmp/bench "1:8 4:8 8:8"
# Runs the packages in packages.txt once per spec, leasing PostgreSQL test
# databases from ~/.config/juju-pg-poc/db-pool-<n> at go test -p <p>.
set -u
here=$(cd "$(dirname "$0")" && pwd)
out=$1
specs=$2
mkdir -p "$out"
cd "$(git -C "$here" rev-parse --show-toplevel)" || exit 1
unset JUJU_POSTGRESQL_TEST_URL
pkgs=$(sed 's#^#./#' "$here/packages.txt")
go test -count=1 -run '^$' $pkgs 2>&1 | cat > "$out/warm.out"
echo "warm rc=${PIPESTATUS[0]}"
for spec in $specs; do
  n=${spec%%:*}
  p=${spec##*:}
  tag="n$n-p$p"
  export JUJU_POSTGRESQL_TEST_URL_POOL=~/.config/juju-pg-poc/db-pool-$n
  rm -f "$out/$tag.sample"
  "$here/pool-sample.sh" "$out/$tag.sample" &
  sp=$!
  start=$(date +%s)
  go test -count=1 -p "$p" -timeout 30m $pkgs 2>&1 | cat > "$out/$tag.out"
  rc=${PIPESTATUS[0]}
  end=$(date +%s)
  kill "$sp"
  echo "$tag rc=$rc wall=$((end-start))s" \
    "ok=$(grep -cE '^ok\s' "$out/$tag.out")" \
    "fail=$(grep -cE '^FAIL\s' "$out/$tag.out")"
done

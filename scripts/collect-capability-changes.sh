#!/bin/sh
set -eu

workspace=${CAPCHECK_WORKSPACE:-"$HOME/work"}
runs=${CAPCHECK_RUNS:-"${XDG_STATE_HOME:-$HOME/.local/state}/ai-deck-converter/capability-crosschecks"}
mkdir -p "$runs"
runs=$(cd "$runs" && pwd)
previous=
if [ -f "$runs/last-complete" ]; then
  IFS= read -r previous < "$runs/last-complete"
  if [ ! -d "$previous" ]; then
    echo "Missing completed run: $previous" >&2
    exit 1
  fi
fi
run=$(mktemp -d "$runs/$(date -u +%Y%m%dT%H%M%SZ).XXXXXX")

collect() {
  key=$1 slug=$2 branch=$3
  repo="$workspace/${slug##*/}"
  if ! git -C "$repo" rev-parse --git-dir >/dev/null 2>&1; then
    echo "Missing Git checkout: $repo ($slug)" >&2
    exit 1
  fi
  remote="git@github.com:$slug.git"
  ref=$(git -C "$repo" ls-remote --exit-code "$remote" "refs/heads/$branch")
  head=$(printf '%s\n' "$ref" | cut -f1)
  git -C "$repo" fetch --no-tags --no-write-fetch-head "$remote" "$head" >&2
  printf '%s\n' "$head" > "$run/$key.sha"
  old=-
  mode=inventory
  if [ -n "$previous" ]; then
    old=$(cat "$previous/$key.sha" 2>/dev/null || true)
    if [ -z "$old" ]; then
      old=-
      mode=rebaseline
    elif git -C "$repo" merge-base --is-ancestor "$old" "$head" 2>/dev/null; then
      mode=delta
    else
      mode=rebaseline
    fi
  fi
  printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$key" "$slug" "$branch" "$old" "$head" "$mode" >> "$run/ranges.tsv"
  if [ "$mode" = delta ]; then
    git -C "$repo" log --format='%H %s' "$old..$head" > "$run/$key.commits"
    git -C "$repo" diff --find-renames --name-status "$old" "$head" -- > "$run/$key.paths"
  else
    git -C "$repo" ls-tree -r --name-only "$head" > "$run/$key.paths"
  fi
}

collect gateway Kong/kong-ee aigw-master
collect koko kong-konnect/koko-private main
collect converter Kong/ai-deck-converter main
git -C "$workspace/koko-private" show "$(cat "$run/koko.sha"):go.mod" > "$run/koko-go.mod"
printf '%s\n' "$run"

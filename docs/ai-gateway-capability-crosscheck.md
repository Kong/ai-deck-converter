# SOP for updating the AI Gateway capability matrix

Run this procedure when Gateway, Koko, or the converter changes AI Gateway capabilities. Use the changes since the last completed crosscheck to update the affected rows in RFC 00230. Record the source revisions and any open questions for the next run. [RFC 00230](decisions/00230-support-setting-formats-and-capabilities-all.md)

## 1. Collect the source revisions

Use Git checkouts named `kong-ee`, `koko-private`, and `ai-deck-converter` under `~/work`. Set `CAPCHECK_WORKSPACE` if they live elsewhere. The collector needs Git read access to all three repositories. For a missing Koko checkout, for example:

```sh
git clone --filter=blob:none --no-checkout --single-branch --branch main \
  git@github.com:kong-konnect/koko-private.git ~/work/koko-private
```

From the root of the converter repository, run the [change collector](../scripts/collect-capability-changes.sh). It pins the canonical branches without switching your local branches.

```sh
run=$(./scripts/collect-capability-changes.sh) &&
  cat "$run/ranges.tsv" &&
  rg -n 'github.com/Kong/ai-deck-converter' "$run/koko-go.mod"
```

Each row records the source, repository, branch, previous SHA, current SHA, and mode. The collector stores runs under `${XDG_STATE_HOME:-$HOME/.local/state}/ai-deck-converter/capability-crosschecks`. Set `CAPCHECK_RUNS` to use another directory. The Koko dependency pin tells you which converter Koko consumes, even when converter `main` is ahead.

| Mode | What to do |
|---|---|
| `inventory` | First run. Use the complete file lists to establish the matrix for the supported runtimes. |
| `delta` | Read the commit list and the complete changed-path list for each source. Equal SHAs produce empty lists. Revisit findings carried over from the previous run. |
| `rebaseline` | The previous SHA is missing or is not an ancestor. Check for a shallow clone or rewritten history. Recover the history and rerun, or rebuild the inventory for that source. |

## 2. Identify candidate capability changes

Start with the files below. Read the full `*.paths` lists as well: a new plugin, renamed driver, or test outside these directories can introduce a capability. For each candidate, inspect its commit and the diff at the pinned SHAs, including removals. A test-only change can clarify an existing rule without adding support.

| Source | Inspect | Question it answers |
|---|---|---|
| Gateway | `kong/llm/schemas/`, `kong/llm/drivers/`, adapters, AI proxy and selector plugin schemas, changelogs, and request tests | Which route types, methods, paths, formats, and providers work? Which target options or transport restrictions apply? |
| Koko | `internal/server/ai-gateway/openapi.yaml`, model validation, runtime compatibility rules, integration tests, and `go.mod` | Which resource type may declare this capability? What do wildcard, omitted, and explicit values mean at the control plane? |
| Converter | `internal/aimap/{endpoints,support,versions}.go`, `convert/model_endpoints.go`, `revert/`, forward goldens, and DB-less fixtures | What routes and plugins does each input actually produce? What does reverse conversion reconstruct? |

For example, use this in `delta` mode to read the Gateway changes. Repeat for Koko and the converter with the relevant paths.

```sh
gateway_repo="${CAPCHECK_WORKSPACE:-$HOME/work}/kong-ee"
old=$(awk '$1 == "gateway" {print $4}' "$run/ranges.tsv")
new=$(cat "$run/gateway.sha")
git -C "$gateway_repo" diff "$old" "$new" -- kong/llm kong/plugins
```

## 3. Reconcile each affected matrix row

Start `$run/findings.md` with the open items from the previous completed run. Add one row per affected format, capability, endpoint, and provider combination. Distinguish **added**, **removed**, **restricted**, **expanded**, and **documentation or test clarification**. On the first run, derive the combinations from the source inventories and compare them with every RFC row in both directions; looking only at existing RFC rows misses new endpoints.

| Capability and resource type | Client format and provider | Method, path, route type, conditions | Previous → current behavior | First verified runtime | Converter output and RFC section | Evidence and action |
|---|---|---|---|---|---|---|
| One combination per row | Include native or translated format | Include relevant target options | Describe the observable difference | Release, branch-only, or unknown | Record agreement or the exact discrepancy | Pinned source lines, decision, follow-up |

Verify runtime availability against a release tag, release metadata, or a matching data-plane image. A change on `aigw-master` alone supports a **branch-only** finding. Preserve older runtime rows when a later release removes support. If Koko accepts a value before the Gateway serves it, or the converter pin lags `main`, record each layer’s behavior separately.

## 4. Verify behavior and update the RFC

On a converter checkout at the recorded SHA, use the smallest input that distinguishes the old and new behavior. Compare explicit capability selection with `*`, exercise both resource types where relevant, and include a supported target plus an unsupported one. At a version boundary, check the release below the gate and the first supported release. Run the CLI with `-direction to-dbless` and inspect the route path, methods, plugin scope, target set, and model alias. If the claim concerns request forwarding, verify the upstream request path with a driver test or a live Gateway request.

For each case, choose the fixture and minimum runtime version, then save the generated config and any conversion issues:

```sh
case_name=104_wildcard_capabilities_multi_format
fixture="convert/testdata/$case_name/input.yaml"
runtime=2.2
go run ./cmd/ai-deck-converter -direction to-dbless \
  -min-runtime-version "$runtime" "$fixture" \
  > "$run/$case_name-$runtime.yaml" 2> "$run/$case_name-$runtime.issues"
```

Apply each confirmed delta to its RFC section: update provider/capability rows and conditions, runtime gates, wildcard rules, examples, and verification cases wherever the change affects them. Mark unreleased behavior explicitly. If converter output disagrees, document the discrepancy and link the implementation follow-up. A clean converter test suite alone establishes no new upstream support. Include commit permalinks in the run notes; use references appropriate to the RFC’s audience in the RFC itself.

## 5. Save the checkpoint for the next run

Record the RFC diff or PR, the verified outcomes, and any unresolved item with its next action in `$run/findings.md`. For an empty run, record which ranges were empty or why the changes leave the matrix intact. Advance the checkpoint after all three sources have been examined and every candidate has a disposition. Keep open findings in the next run even when their source SHA is unchanged.

```sh
runs_dir=$(dirname "$run")
printf '%s\n' "$run" > "$run/next-checkpoint"
mv "$run/next-checkpoint" "$runs_dir/last-complete"
```

Next time, rerun step 1. The collector reads `last-complete` and compares each source with its own last checked SHA. An interrupted run leaves the previous checkpoint available.

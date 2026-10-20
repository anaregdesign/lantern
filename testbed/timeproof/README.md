# OFF configured-source proof

This is a separate, opt-in normal-build OFF proof. It does not replace OIDC's
schema-3 proof, controlled functional validation, or release performance gates.
Execution currently selects Linux arm64/amd64 with the existing read-only
`/etc/ntp.conf` containing exactly `server time.asia.apple.com` and a newline.
It never changes host settings, time services, privileges or network policy.

Prepare from a clean exact source, using the repository-pinned Go binary. The
preparation directory must be new and outside the checkout. This builds the
normal App test child and existing fixture provisioner from a read-only Git
export (excluding ignored inputs), verifies cached modules, then checks identity
without constructing a source owner:

```sh
python3 -B testbed/timeproof/off_source.py prepare --root "$PWD" \
  --prepared /absolute/new/preparation --go /absolute/pinned/bin/go
```

The returned preparation digest is a trusted input for the one allocated run.
Only after its execution scope and selected environment are approved:

```sh
python3 -B testbed/timeproof/off_source.py run --root "$PWD" \
  --prepared /absolute/new/preparation --prepared-sha256 PREPARATION_DIGEST \
  --evidence /absolute/new/evidence --execute
```

The run consumes its preparation allocation even on failure. It starts three
children once, retaining their own reserved listeners and production App/Wire
composition. The complete launch-through-wait window is at most120 seconds;
work is canceled by90 seconds. Each owner keeps the existing45-second first
anchor wait and3-second exchange. All startup/retry/background transactions
count toward aggregate attempts/writes <=12. Only complete, stable post-join
rows with positive installations and no failed/denied/in-flight owner qualify.
Timeout, kill, missing counters, source drift and failed wire checks remain
FAIL. Missing startup counters are unavailable, not zero. There is no automatic
retry, replacement child, environment switch or threshold change.

Preserve the command's receipt digest through a trusted same-run channel. A
receipt cannot attest its own trust. Verify it against the same clean source:

```sh
python3 -B testbed/timeproof/off_source.py verify --root "$PWD" \
  --prepared /absolute/new/preparation --prepared-sha256 PREPARATION_DIGEST \
  --evidence /absolute/new/evidence --trusted-receipt-sha256 RECEIPT_DIGEST
```

The verifier checks binary/source binding, raw child transcripts, phase order,
App and Wire completion, closed workload refusal, exact final counts and child
exit/wait. Reaping a killed process does not establish graceful join. Readiness
and replication reads may poll within the fixed window; no source preflight
probe or source-owner restart occurs. Final counters prove actual transaction
accounting; passive polling is not an atomic network transaction limiter.

Network-free tests use synthetic receipts/processes, not a real time source:

```sh
python3 -B -m unittest discover -s testbed/timeproof -p '*_test.py'
```

Builds tagged `lantern_test_time` omit the normal child entry entirely. The
normal child's identity-only operation returns before any listener, App,
native sampler or source owner is constructed. Controlled-source mechanism
tests remain functional evidence and cannot qualify this normal-source run.

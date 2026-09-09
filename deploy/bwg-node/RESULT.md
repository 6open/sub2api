# BWG Shared-State Pilot: Rejected

Evaluated on 2026-09-07. This is a completed deployment experiment, NOT an
accepted public gateway. Per the approved rollback condition, the pilot was
withdrawn after the latency comparison failed. Do not publish the DNS name as
a usable API endpoint.

## Configuration

- Isolated source worktree based on deployed commit `315f63df9`.
- Backend only, normal billing mode, admin user ID 1 only, OpenAI group only.
- Global admission limit 10, request limit 8 MiB, MemoryMax 300 MiB.
- Shared PostgreSQL and Redis through a dedicated private SSH tunnel.
- Dedicated database login without DDL or DELETE grants.
- Request/audit/account selection and token pricing reused from LKLB.
- Local durable billing outbox feeds existing idempotent billing and usage-log
  repositories. No prompts or credentials are included in billing entries.

## Actual Measurements

All three requests used admin key ID 108, account ID 6, `gpt-5.6-sol`, low effort,
streaming and the same benign input. No API key value is recorded here.

| Entry | Complete Response | Upstream Duration in Usage Log | Cost |
|---|---:|---:|---:|
| BWG private node, first request | 25.782643 s | 4.230 s | $0.0004638000 |
| Original LKLB, called from BWG | 4.239984 s | 3.486 s | $0.0004638000 |
| BWG private node, second request | 22.640144 s | 4.796 s | $0.0004638000 |

Corresponding billing request IDs:

- `client:73338b01-7171-4312-ae05-d3fdef7f8b71`
- `client:0537526b-8968-451f-8ffe-251f263a1371`
- `client:25b43ae6-2015-4d0b-8e32-d9841dcef25b`

The node's measured memory peak was 37,560,320 bytes (about 35.8 MiB). This was
a small-request pilot, not a ten-way maximum-body memory stress test. The large
extra latency was outside the measured upstream call; the exact database/Redis
breakdown was not instrumented. Sharing state across the ocean was not a latency
win in this implementation.

The original audit endpoint uses `host.containers.internal:19110`, which is not
reachable from this standalone BWG process. The benign pilot request logged
`prompt_guard_unavailable` and followed the existing fail-open policy. This also
needs resolution before any future release; auditing must not be claimed verified.

## Tests and Limits

- Race/unit tests passed for the durable outbox, simulated disconnect/restart
  and at-most-once simulated billing effects.
- The maintenance-provider isolation test passed.
- Real requests produced matching costs and usage records in the shared ledger.
- The outbox had no pending entries before rollback.
- Full live fault injection, public TLS, and peak-load acceptance were NOT run
  after the pilot failed the latency criterion.

Additional maintenance cleanup loops were observed in constructors and in the
audit writer despite provider-level startup suppression; DELETE permission was
denied by the dedicated role. Future builds must isolate these loops as well.

## Rollback

- Stop/disable `lklb-bwg-node.service` and `lklb-bwg-tunnel.service` on BWG.
- Keep the earlier `lklb-edge.service` prototype stopped/disabled.
- Stop `lklb-edge-state-relay` on ali98.
- Disable login for the dedicated database role and remove only the new tunnel
  public-key line, preserving other SSH authorizations.
- Remove the BWG runtime environment and tunnel private/public-key files, and
  remove the local temporary copy of the admin test API key. The real API key
  in LKLB remains unchanged. Private local deployment backups are retained.
- Keep source, binaries and private deployment backups for inspection; do not
  alter or delete existing users, API keys, usage rows or billing records.
- Public 9443 was never opened, no TLS certificate was issued, DNS is retained.
- LKLB's production binary, main nginx routes and the BWG proxy configuration
  were not changed. Migo was not touched.

Next architecture decision: localize the control plane/state in the US, or
implement a complete edge control protocol with audited authorization and durable
settlement. Adding RAM alone cannot remove cross-region state-query latency.

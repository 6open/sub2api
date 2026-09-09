# LKLB US Pilot

Validated and opened on 2026-09-07.

Base URL: `https://us.lklb.top/v1` (legacy `:9443` remains available).

TCP 443 is now handled by nginx stream/SNI: `us.lklb.top` routes to the
loopback TLS listener 10444, other SNI routes to Xray on loopback 10443.
Both receive PROXY protocol to retain client IP. Xray's VLESS/Reality identity
and client credentials are unchanged. UDP 443/sing-box is unchanged.
Backup: `/root/bwg-sni-backup-20260907` on BWG. Restore the original nginx and
Xray configs together when rolling back SNI; do not stop sing-box.

The pilot accepts LKLB user IDs 1 (admin) and 157 (icebear_j), using an active, unexpired,
unlimited OpenAI-group API key without per-key spending-window limits.
Admin key ID 108 was used for acceptance; no key value is stored here.

User 157 was subsequently added to the identity allowlist. This is not a billing
exemption: the main gateway's existing balance, account state and admission
checks remain in the authorization pipeline, and settlement is attributed to the
actual signed user and API key. Disabled keys are not reactivated. Global edge
concurrency is 20 across both users. Other users remain denied.

User-157 live verification: active key 197 returned a complete GPT-5.6-sol
response in 5.283 seconds; `edge:433134407a53074fbfa8dcfa8e1c1044` produced exactly
one usage row for user 157/key 197, cost $0.0004638 at multiplier 0.15. His inactive
key returned 401, an unrelated user's key returned 403, and admin remained 200.

## Supported Surface

2026-09-08 TTFT correction: controller maps legacy zero/unmeasured text latency
to a nil FirstTokenMs before usage recording. Wire receipts remain unchanged
for hash/replay compatibility. The existing usage UI renders NULL as a dash.
400 historical edge rows were backed up in ops_edge_ttft_backup_20260908 and
only first_token_ms was changed to NULL. Original screenshot request
edge:4097efb131897b67cb4a800a09d234ad retains its duration and billing.
Positive latency is covered by unit tests and a live text request; forced-tool
live probes hit upstream overload, so successful tool-only live acceptance is
not claimed.

2026-09-08 full-catalog acceptance: admin and user 157 both match main-host
catalogs (21 normal models, 8 Codex catalog entries for client_version 0.144.1).
Terra, Luna and GPT-5.5 Responses completed through US execution. Sol Chat
Completions and GPT-Image-2 low-quality 1024x1024 generation completed through
main relay, with one generated image. An unapproved user's relay request was
denied with 403. Other models depend on main-host account capacity and have not
each been separately load-tested; audio/WebSocket paths were not live tested.

2026-09-08 stream-fix acceptance: race tests cover silent upstream timeout,
continuous heartbeat bytes reaching only the total deadline, terminal completion
without EOF, and turn-state registration with the original hyphenated Session-Id.
Service tests cover preserving state for the owning account and stripping it on
account change. Public Astra xhigh requests completed in 5.581s and 3.605s on the
same account. The first returned turn-state; the second echoed it and upstream
issued no replacement (confirmed against persisted receipt headers). Both settled
once. These short probes do not reproduce the user's original long task.

- `GET /v1/models`, including the Codex model catalog query, mirrors the full
  main-host authorized catalog without a two-model filter.
- Compatible GPT-5 family (except Spark) and Astra text/image-input Responses
  requests execute in the US. Main-host policy and account selection apply.
- Other `/v1/` API requests (including chat completions, images, audio, Spark,
  compact, server tools and WebSocket ingress) relay to `https://lklb.top` after
  control-host pilot authorization. Main performs execution and billing once;
  these requests do not get US-to-upstream acceleration. Main-host availability
  and permissions still apply; listing a model is not a guarantee of capacity.
- `X-Lklb-Execution-Route` identifies `us` execution or `main` relay. Forwarded
  main requests do not create edge receipts or duplicate billing. Their main
  access logs see the US proxy's egress IP rather than a trusted original IP.
- Text and image inputs, including historical screenshots, and client-executed
  function/custom/namespace tools. The 8 MiB bound includes base64 and history.
- Image input token details are carried through receipts into main-host billing
  when supplied upstream; otherwise upstream aggregate input-token usage applies.
- SSE and non-stream response conversion; live acceptance used SSE.
- Global node and controller concurrency 20, input body limit 8 MiB.
- Execution total limit 20 minutes; 180 seconds without upstream bytes cancels
  an idle stream. Terminal events end the stream without waiting for socket EOF.
- Codex turn-state headers are relayed after the controller records the owning
  account through an authenticated lease heartbeat. Request IDs are relayed too.
- Incomplete streams persist diagnostic reason, last event category, byte count
  and time since last data, and emit an explicit SSE error. Diagnostics never
  contain prompts, output text, credentials, or turn-state blobs. Unknown usage
  remains uncertain and is not automatically re-executed.
- Large requests also share a 192 MiB reservation budget and can be rejected
  before reaching concurrency 20. Each request reserves 8 MiB plus six times its
  body size. Host available-memory floor is 250 MiB; MemoryMax remains 300 MiB.

All relayed requests retain the node's 8 MiB body bound, concurrency limit and
memory admission guard. Relay connections have a 20-minute total limit.
No client global configuration was changed.

Image-input live acceptance: `edge:dd3a1e05d24d931519a68191cc487c9b` returned
HTTP 200 and correctly read "BytePlus" from an earlier image message when the
latest message was text only (115,710-byte JSON, 5.292 seconds). One usage row
was recorded, cost $0.00183555. Upstream supplied aggregate input tokens but no
separate image-token count in this probe. An oversized JSON request returned 413.

## Request Path

The US node sends authorization input to a control process on the LKLB host.
The control process runs the normal main-host authentication, policy/audit,
model mapping and account selection, then issues a body-bound Ed25519 permit.
It uses the existing joint live concurrency leases, counted by the original
gateway and preserved across ordinary process-slot cleanup.

The US node calls ChatGPT directly and streams responses directly to the client.
It persists usage metadata before forwarding a terminal SSE event, and reports
the receipt to the main-host controller. The controller persists the calculated
billing command before applying the original idempotent billing transaction and
usage-log insertion. Replays reuse the exact command, not a recalculated price.

BWG has no database or Redis credentials. Only the main-host controller accesses
the main-host stores. The original public LKLB process was not replaced/restarted.
Migo was not modified.

## Runtime

- BWG: `lklb-us-gateway.service`, loopback `127.0.0.1:19445`.
- BWG: `lklb-us-link.service`, SSH forwarding `127.0.0.1:29445` to the main-host
  controller's `127.0.0.1:18445`. Its key has a forced command and PermitOpen.
- Main host: rootless Podman container `lklb-edge-control`, user-systemd unit of
  the same name, based on cached Alpine with read-only binary/config mounts.
- Main state: `/home/admin/lklb-edge-control/state/leases`.
- Node state: `/var/lib/private/lklb-us-gateway/requests`.
- Main signing key never leaves the main host for BWG; BWG has the public key.
- BWG config uses systemd LoadCredential. Logs/receipts do not contain API keys,
  upstream bearer credentials, prompts or generated response text.
- Nginx serves TLS through SNI on TCP 443 and directly on legacy 9443.
  Xray uses internal TCP 10443; UDP 443/sing-box is untouched.
- Certificate expiry: 2026-12-06. Renewal hooks temporarily open HTTP 80 for
  ACME validation and close it afterwards, then reload nginx.

The original image could not be rebuilt by the host's old Podman due to a MIME
compatibility error. The active controller instead uses the tested bound binary
in cached `alpine:3.22`, mounted host timezone data, the main data directory read
only, and a private writable state directory. The Dockerfile is a packaging
recipe, not the image actually deployed in this pilot.

## Acceptance Evidence

- External HTTPS without a key: 401, certificate verification successful.
- Original admin-only acceptance: non-admin user 157 returned 403. Following
  explicit authorization, user 157 is now permitted; other users remain denied.
- Real GPT-5.6 streaming request: 200, complete output and matching bill.
- Real GPT-6 streaming request over public TLS: 200, about 3.36 seconds total.
- Real function call: `echo_probe` returned successfully.
- Original ten-slot acceptance: 10/10 complete, all 200, 3.36-4.69 seconds.
- Their ten distinct request IDs produced ten usage rows, not duplicate rows.
- Node memory peak during that test: 14,315,520 bytes (about 13.7 MiB).
  This is not a maximum-body stress result. MemoryMax is 300 MiB.
- Concurrency-20 deployment (2026-09-07): gateway and controller updated;
  race-tested twenty-slot admission and rejection of the twenty-first request.
  Public 443 load test with existing traffic: 18/20 complete, two rejected with
  `Node busy`. All 18 successful request IDs have exactly one usage row each
  (total actual cost $0.0083484000). This was not an isolated 20/20 load test.
  A second burst likewise returned eighteen 200s and two node-busy 503s.
  Existing traffic was not interrupted to free test slots. Pending settlements
  returned to zero; MemoryMax stayed 300 MiB and proxy services remained active.
- Live fault injection stopped only the dedicated control link during generation.
  The client received the complete response, one receipt queued, and restarting
  the node recovered and settled it. Explicitly reposting the receipt did not
  double charge: `edge:6f30e22e8bff8ec4867219391442b5e8` has one usage row,
  cost `$0.0013623000`.
- Pending node settlements were zero at final acceptance.
- Race/unit tests passed for permits, state storage, node replay recovery,
  headerless native SSE and global live-lease concurrency interoperability.
- Original LKLB health remained 200; existing BWG proxy services remained active.

An initial private compatibility probe was rejected because the upstream omitted
the Content-Type header; this was fixed and regression-tested. Its uncertainty
record is retained rather than fabricating usage. More generally, a process loss
before any terminal usage is received cannot reconstruct missing vendor usage:
such requests are retained as `uncertain`, never automatically re-executed or
silently treated as an accurately settled zero-cost request. They require review.

No broad latency reduction is claimed from this small sample. Authorization
still crosses to the main host; only execution and client response delivery are
local to the US node. The previous shared-database-on-BWG trial remains withdrawn.

## Operations and Rollback

Check the node's private `/health` for pending settlement count and inspect the
main lease states when investigating a request. Preserve pending/uncertain files.
The current state stores have a 10,000-record capacity guard; retention/archival
must be handled before expanding beyond this pilot. Do not delete pending bills.

For rollback, stop/disable the BWG gateway service first, preserve or drain queued
receipts, close only TCP 9443, then stop the dedicated link and main controller.
Do not stop sing-box, Xray, the original sub2api container, PostgreSQL or Redis.
Keep ledger/receipt state until reconciled; remove only the dedicated SSH key when
decommissioning. The original client endpoint remains available throughout.

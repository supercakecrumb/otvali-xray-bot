# Private beta feedback

The Mini App lets a bound Otvali user report their own experience without
issuing or changing VPN credentials. It uses the existing one-hour Telegram
initData authentication, exact Origin allowlist, private no-store responses,
request quotas and bounded concurrency. Every action rechecks the current
numeric Telegram binding; administrator actions also check the stored role.

The private-chat commands `/beta` and `/start beta` open the beta entry. A link
`https://t.me/<bot_username>?start=beta` works with either configured bot. Both
launch signatures resolve the same existing account and feedback history.

There is no unauthenticated form, third-party form provider, screenshot upload,
IP lookup, exact-location collection or automatic tester invitation. Do not
paste credentials or raw logs. Cities/providers may be entered as «Не знаю».
A report is user testimony, not proof of packet delivery or an automatic test.

## API

All actions use POST, `Authorization: tma <initData>` and JSON like the existing
Mini App. Query parameters are rejected. Request bodies are at most 16 KiB;
unknown fields, non-object JSON, invalid UTF-8/control characters and oversized
text are rejected. Telegram/user IDs cannot be chosen in request bodies.

| Path after `/api/miniapp/` | Body | Response |
| --- | --- | --- |
| `beta/plan` | `{}` | `{plan}`: authenticated routes, test steps, privacy notice, current `is_admin` |
| `beta/report` | submission below | `{report,duplicate}` |
| `beta/reports` | `{scope:"mine"\|"admin",before_id?:number}` | `{reports,next_before_id?}`; newest first, 25/page |
| `beta/report/detail` | `{id:number}` | `{report}`; own or admin |
| `beta/report/update` | `{id:number,status:string,note:string}` | `{report}`; admin only |

Submission fields:

- `request_id`: lowercase UUID v4 generated once per submission; reuse on retry.
- `route_id`: one returned by plan (`sto`, `fi`, `msk-sto`, `msk-fi`,
  `legacy_fi`, `other`). Managed routes require current eligibility; legacy
  requires existing access policy. Plan reads no keys and provisions nothing.
- `app`: `incy`, `happ`, `other`; `app_version` optional, 40 Unicode characters.
- `os`: `android`, `ios`, `windows`, `macos`, `linux`, `other`.
- `device`: optional, 100 characters; `city`: required, 80 characters.
- `network_type`: `wifi`, `mobile`, `wired`, `other`.
- `provider`: required, 100 characters; no precise address required.
- `symptom`: `works`, `cannot_connect`, `timeout`, `no_internet`, `slow`,
  `unstable`, `partial`, `other`.
- `results`: each of the six plan IDs exactly once as `{id,state}`. States:
  `pass`, `fail`, `slow`, `not_tested`; at least one must be tested.
- `comments`: optional, 2000 characters; newlines permitted.

Tests cover connection, RU browsing, foreign browsing, actual YouTube playback,
Telegram messages to Saved Messages and Telegram media upload/download. Own
infrastructure/HA/service addresses are never in the plan or public assets.

Report fields include the submitted content, numeric report `id`, UTC ISO
`created_at`/`updated_at`, `status` and `note`. Request ID and payload hash are
never returned. Only administrators receive `author:{username,telegram_id}`.
A user's own report list never includes other reports or author identifiers.

Statuses: `new`, `investigating`, `need_info`, `resolved`. `note` is explicitly
an **answer visible to the reporting user**, not an internal staff note.
`need_info` requires a nonempty answer. No Telegram message is sent on submit or
triage; users see status/reply in the Mini App. A second report can record a
retest without altering the first observation.

Errors use the existing `{error:{code}}` envelope: `invalid_request` (400),
`sensitive_data` (400), `unauthorized` (401), `forbidden` (403), `not_found`
(404, also other users' report IDs), `conflict` (409), `rate_limited` (429),
`unavailable` (503). Sensitive text checks reject links, credential formats,
UUIDs, IP addresses and likely pasted secrets rather than silently rewriting a
report. This is a guard against accidental pasting, not a complete PII detector.
Errors and query logs contain no request bodies or raw DB errors.

## Persistence and retry safety

Startup adds the separate `beta_reports` table and indexes. It does not rewrite
users, server rows, keys, subscriptions or grants. `user_id` is derived from the
current binding. The `(user_id,request_id)` unique index scopes deduplication.
The canonical content hash ignores object/result order and trims outer text
whitespace. An identical retry returns the original report; a changed payload
under the same ID returns 409 and preserves the original.

The transaction locks the bound user row before checking deduplication and
rolling limits (12 new reports/hour, 60/day). Concurrent submits cannot bypass
those limits; harmless identical retries remain possible at the limit. Existing
HTTP limits also remain: 20 requests/user/minute, 300 global/minute, 8 in flight.
Report storage logs are disabled. No VPN key/URL, initData or device IP is stored
in this table. Reports inherit the app database's existing private backup and
access controls. No automatic deletion is configured in this first beta.

The optional activity collector records successful response preparation only,
not proof that a client received or read a response. `miniapp_beta_submit` is
recorded only for a new report; successful duplicate delivery retries are not
counted as another report. Failure of metrics never changes the form result.

## Verification

`go test ./internal/miniapp ./internal/database` exercises API auth, unknown
fields, cross-user isolation, administrator revocation, canonical retry and
conflicts, secret/control/text validation, route eligibility and pagination.

The opt-in `TestBetaStorePostgres` requires a newly empty disposable database
named `otvali_beta_ci_*` via `BETA_TEST_DATABASE_URL`. It checks additive
migration/preserved users, SQL ACLs, current admin role, author joins, concurrent
idempotency, quota serialization and bounded reads. It refuses other database
names and does not create/drop a database itself. Never pass production access.

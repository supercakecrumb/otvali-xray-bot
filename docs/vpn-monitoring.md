# VPN connection monitoring

Monitoring uses real, dedicated VPN configurations on the Home Assistant host.
It does not inspect customer traffic or retrieve customer keys when someone opens
the status page. The existing account-access check is still available as
`/account` and **Мой доступ**. When monitoring is disabled, `/status` keeps its old
account-access behavior.

## User experience

`/status` and **Состояние VPN** show the last stored connection results, the
timestamp in Moscow time, and two public categories: Russia and foreign internet.
Bound administrators also see the private services category. **Подробнее по маршрутам** opens a short card for a chosen route.
**Обновить статус** reloads the stored result; it never starts another probe.

The public view says **Проверка из России**. All own-service checks—including
the guide, API and subscription—are hidden from ordinary users. Home Assistant,
the home management panel, media and downloads are visible only after checking
the requesting Telegram ID is currently a bound administrator. The complete
private category is removed before public rendering; its checks, summary icons,
counts and failure hints cannot alter the public result. Alert delivery remains
limited to the separately configured owner; reading admin diagnostics does not
subscribe an administrator to alerts. Reports contain no raw URLs, addresses,
subscription links, VPN credentials or Telegram identities.

The test is one vantage point and one dedicated configuration per route. A green
status is not a claim that every customer's device or provider is working. The
reported duration is application response time, not bandwidth or a speed test.
Version 2 reports actual sent/received byte counts and verifies complete test
payloads. YouTube covers the website and a thumbnail; Telegram covers an initial
MTProto exchange. These do not prove video playback, calls or raw IP packet-loss
percentages. Unattempted checks display the concrete collection/setup reason;
old results are never silently carried forward as fresh.

## Receiver and contract

The existing Mini App HTTP listener exposes `POST /api/monitor/report`. Enable
`VPN_MONITOR_ENABLED=true` only together with `MINIAPP_ENABLED=true`, a private
32–256 character `VPN_MONITOR_SECRET`, and a positive numeric
`VPN_MONITOR_ALERT_CHAT_ID`. Startup fails unless that ID is a bound admin.
There is no fallback notification recipient.

The collector sends JSON and these headers:

- `Content-Type: application/json`
- `X-Otvali-Timestamp`: Unix seconds, decimal, at most five minutes old or one
  minute in the future.
- `X-Otvali-Signature`: hexadecimal HMAC-SHA256 of the exact timestamp, a newline,
  then the exact request body, using `VPN_MONITOR_SECRET`.

JSON fields:

```json
{
  "version": 2,
  "source": "home-assistant",
  "checked_at": "2026-10-26T12:00:00Z",
  "interval_seconds": 300,
  "routes": [
    {
      "id": "stockholm",
      "kind": "vpn",
      "state": "ok",
      "checks": [
        {"id": "ru-yandex", "category": "ru", "state": "ok", "latency_ms": 45, "http_status": 200, "error_code": "", "attempts": 1, "successes": 1, "bytes_received": 1200, "bytes_sent": 0, "verified_body": true}
      ]
    }
  ]
}
```

The example above is abbreviated. Every report must contain **all six routes**
and **all sixteen checks per route** for version 2, without duplicate or unknown
IDs. Version 1 remains accepted with its exact original twelve checks and no
transfer fields, so the receiver can be deployed before the collector:

| Route ID | Kind |
| --- | --- |
| direct | direct |
| stockholm | vpn |
| finland | vpn |
| msk-stockholm | vpn |
| msk-finland | vpn |
| legacy-finland | vpn |

| Check IDs | Category | Visibility |
| --- | --- | --- |
| ru-yandex, ru-vk | ru | Everyone |
| world-cloudflare, world-google, world-exit | world | Everyone |
| world-youtube, world-telegram, world-download, world-upload (v2 only) | world | Everyone |
| own-guide, own-api, own-subscription | own | Bound admins only |
| own-home-assistant, own-home-dokploy, own-jellyfin, own-qbit | own | Bound admins only |

Check states are `ok`, `fail`, `unknown`. Route state is a compatibility field
with `ok`, `degraded`, `down`, `unknown`; the receiver recomputes every displayed
state from individual checks. `label` is optional and ignored for rendering.
`error_code` accepts only `[a-z0-9_]{0,48}`; it must never carry raw errors or URLs.
Latency is an integer from 0 to 120000 ms; HTTP status is 0–599. Unknown checks
must be present when configuration, source routing, or collection is unavailable.
`world-exit` verifies that the observed exit matches the intended route without
including the address in the report.

Version 2 adds `bytes_received` and `bytes_sent` (0–10 MiB each), `attempts` and
`successes` (0–10, successes never above attempts), and `verified_body`. Known
checks require an attempt; successful checks require all attempts to succeed.
Unknown checks have no attempts. Verified application data requires received
bytes and at least one successful attempt. The four new application/transfer
checks cannot be green without verified response content.

Bodies are limited to 96 KiB and two concurrent receiver requests. Unknown JSON
fields, missing matrix entries, queries and wrong methods are rejected. A report
timestamp must be no more than ten minutes old or one minute in the future. The
same timestamp and byte-identical body are idempotent; older/conflicting reports
return 409. New observations must be at least four minutes apart so retrying
cannot confirm an incident. Preserve the same `checked_at` and body on retries;
the authentication timestamp can change. Successful acceptance returns 200 and
`{"ok":true}`. The receiver never returns observations or raw error payloads.

## Bounded bidirectional data check

With monitoring enabled, `POST /api/monitor/transfer/download` and
`POST /api/monitor/transfer/upload` accept only authenticated synthetic data.
Both require `Content-Type: application/octet-stream`, the same timestamp limits
as reports, and a domain-separated signature:
`HMAC-SHA256(secret, timestamp + "\n" + path + "\n" + exact body)`.
A report signature cannot authorize a transfer. No query parameters, arbitrary
sizes, destinations, files or customer content are accepted.

- Download accepts exactly 32 random nonce bytes. It returns exactly 262144 bytes:
  `SHA256(b"OtvaliProbe/2.0\n" + nonce)` repeated 8192 times. The collector checks
  the complete payload against that expectation.
- Upload accepts exactly 65536 synthetic bytes. It returns only a JSON receipt
  with `size` and the hexadecimal `sha256` of the received body. The collector
  checks both, proving that the server received the entire upload.

The handler permits six concurrent requests and an authenticated token bucket
of twelve requests with one token replenished per second. Replay therefore cannot
create unbounded bandwidth amplification. Responses are `no-store`; bodies,
headers and hashes are not logged or stored. The probes measure the selected
VPN path to this controlled endpoint, not every destination on the internet.

## Incidents and delivery

Two consecutive observations of a known failure confirm a category problem; two complete good
observations confirm recovery. Partial external-target failure is degraded,
not a complete route outage. Unknown targets interrupt a category's streak when
there is no conclusive failure; missing data cannot hide a known failed target.
An all-unknown route has its own configuration/probe incident. Collection gaps
over ten minutes also interrupt streaks.

If every route including the direct baseline loses both internet categories,
the alert identifies a possible home connectivity problem rather than blaming
six independent VPN routes. Baseline failure alone never suppresses a VPN
failure. A failing own service does not make the internet headline report a
VPN outage. Details remain available per route.

After fifteen minutes without a fresh report, the UI shows unknown instead of
old green results, and the owner receives one separate source alert. Two fresh
cycles recover it. No periodic reminders are sent for unchanged incidents.

The additive `vpn_monitor_states` table stores a singleton snapshot, debounce
state and notification outbox in one transaction. The owner ID is checked against
the bound admin row again before each delivery. The dedicated Telegram client
has a fifteen-second HTTP timeout. Failed delivery retains its outbox, with
persisted exponential retry delays up to fifteen minutes. Delivery is at least
once: a crash after Telegram accepts a message but before the database ack can
cause one duplicate. The outbox is bounded to 128 recent transitions if Telegram
remains unavailable for an extended period.

## Operations and tests

Deploy the receiver before starting the collector. Keep the shared HMAC key in
private configuration on both systems. Rotate both together; no key appears in
logs, source or responses. Disable the feature and stop the collector to roll
back; the separate additive database table can remain for recovery.

Run the normal Go suite without a real environment. Optional integration test
`TestMonitorPostgresRestartOutboxAndShutdown` uses only
`VPN_MONITOR_TEST_DATABASE_URL` pointing to a newly created empty database named
`otvali_monitor_ci_<suffix>`. It refuses other database names, tests restart and
concurrent idempotence, failed-delivery retry, recovery and shutdown, and never
creates or drops a database. Do not point it at production.

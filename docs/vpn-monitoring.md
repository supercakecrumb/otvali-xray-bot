# VPN connection monitoring

Monitoring uses real, dedicated VPN configurations on the Home Assistant host.
It does not inspect customer traffic or retrieve customer keys when someone opens
the status page. The existing account-access check is still available as
`/account` and **Мой доступ**. When monitoring is disabled, `/status` keeps its old
account-access behavior.

## User experience

`/status` and **Состояние VPN** show the last stored connection results, the
timestamp in Moscow time, and three categories: Russia, foreign internet, and
Otvali services. **Подробнее по маршрутам** opens a short card for a chosen route.
**Обновить статус** reloads the stored result; it never starts another probe.

The public view says **Проверка из России**. It shows only the guide, API and
subscription checks in its services category. Home Assistant, the home management
panel, media and downloads checks are visible only to the explicitly configured
owner, while that Telegram ID remains a bound administrator. Their failures do
not change the public services summary. Reports contain no raw URLs, addresses,
subscription links, VPN credentials or Telegram identities.

The test is one vantage point and one dedicated configuration per route. A green
status is not a claim that every customer's device or provider is working. The
reported duration is HTTP response time, not bandwidth or a speed test.

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
  "version": 1,
  "source": "home-assistant",
  "checked_at": "2026-10-26T12:00:00Z",
  "interval_seconds": 300,
  "routes": [
    {
      "id": "stockholm",
      "kind": "vpn",
      "state": "ok",
      "checks": [
        {"id": "ru-yandex", "category": "ru", "state": "ok", "latency_ms": 45, "http_status": 200, "error_code": ""}
      ]
    }
  ]
}
```

The example above is abbreviated. Every report must contain **all six routes**
and **all twelve checks per route**, without duplicate or unknown IDs:

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
| own-guide, own-api, own-subscription | own | Everyone |
| own-home-assistant, own-home-dokploy, own-jellyfin, own-qbit | own | Owner only |

Check states are `ok`, `fail`, `unknown`. Route state is a compatibility field
with `ok`, `degraded`, `down`, `unknown`; the receiver recomputes every displayed
state from individual checks. `label` is optional and ignored for rendering.
`error_code` accepts only `[a-z0-9_]{0,48}`; it must never carry raw errors or URLs.
Latency is an integer from 0 to 120000 ms; HTTP status is 0–599. Unknown checks
must be present when configuration, source routing, or collection is unavailable.
`world-exit` verifies that the observed exit matches the intended route without
including the address in the report.

Bodies are limited to 96 KiB and two concurrent receiver requests. Unknown JSON
fields, missing matrix entries, queries and wrong methods are rejected. A report
timestamp must be no more than ten minutes old or one minute in the future. The
same timestamp and byte-identical body are idempotent; older/conflicting reports
return 409. New observations must be at least four minutes apart so retrying
cannot confirm an incident. Preserve the same `checked_at` and body on retries;
the authentication timestamp can change. Successful acceptance returns 200 and
`{"ok":true}`. The receiver never returns observations or raw error payloads.

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

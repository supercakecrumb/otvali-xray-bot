# Private activity counters

Collection starts when `activity_collections` row 1 is first created. Its UTC
timestamp survives restarts. There is no historical backfill: existing key rows
or traffic counters cannot reveal how often someone requested a key.

`activity_events` contains only the local bound user ID, source, a fixed event
name, an opaque deduplication ID and timestamp. It stores no message text,
command arguments, callback payload, credential ID, URL, IP or device data.
Only an existing numeric Telegram binding may create an event; writes use a
250 ms deadline and failures never turn a successful credential action into an
error. Counts are best-effort lower bounds if recording fails; they are not a
billing ledger. Query logging is disabled for the counters.

Telegram categories are disjoint: `message`, `command`, `callback`. Incoming
messages = message + command; incoming actions = message + command + callback.
These are recorded only after the private-chat and existing-user middleware.
`key_delivered` and `subscription_delivered` are separate outcomes recorded
only after Telegram accepts the credential-bearing message/edit/photo. They
include QR and text retrieval; listing available keys does not count as a
delivery. A delivery is not proof of import or a VPN connection. An identical
Telegram update ID is deduplicated per bot and event kind even after a restart.
The old bot retains its original digest format so a replay after migration
still deduplicates against historical events. Its numeric ID is bound once in
`activity_collections.legacy_telegram_bot_id`; token rotation and changing the
notification primary cannot reassign this namespace. Additional bots use their
stable numeric bot ID in each digest. The first dual-bot deployment must keep
the existing old bot in `TELEGRAM_TOKEN` so the initial binding is correct.

Mini App categories count successful API response preparation: session and key
list views, individual key responses, subscription responses, beta plan/list/
detail views and administrator updates. New beta submissions are counted once;
an idempotent duplicate submission does not add an event. Other HTTP retries
are distinct successful operations because these existing retrieval endpoints
have no request-id contract. Key responses and subscription responses remain
separate. Preparation does not prove that a browser received the response.

For a candidate roster, show saved `user_messages` history separately: it covers
ordinary support text, not all commands/buttons, and can predate these counters.
Do not add old support counts to new incoming-message counts because they
overlap after collection begins. Never present unavailable historical key or
action counts as zero. These metrics and candidate lists are administrator-only;
no public endpoint or automatic user broadcast is added.

Run the guarded integration test against an empty disposable database named
`otvali_activity_ci_*` via `ACTIVITY_TEST_DATABASE_URL`. It verifies concurrent
deduplication, immutable collection start, concurrent bind-once namespace
ownership and preservation of users.

Support messages now record nullable `bot_id` and `telegram_chat_id` columns;
historical rows keep their original values and unknown scope remains NULL.
Each admin copy has a separate `user_message_forwards` entry keyed by bot ID,
admin chat ID and Telegram message ID. Admin forwards use the primary bot;
replies return through the saved origin bot. Historical replies are accepted
only by the old primary bot when its own canonical forward header identifies
the same recipient and there is exactly one matching historical row. Missing
or ambiguous references fail closed. No historical chat identity is guessed.
`MESSAGE_TEST_DATABASE_URL` with an empty `otvali_message_ci_*` database checks
repeat migration preservation, cross-bot/chat isolation and immutable forwards.

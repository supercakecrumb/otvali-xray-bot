# otvali-xray-bot
Bot for distributing vpn

## Retiring a server from the bot

Set `BOT_DISABLED_SERVER_IDS` to a comma-separated list of positive server IDs
from the bot's database, then restart the bot. An unset or empty value keeps all
servers enabled. Invalid values stop startup before any database or server
connection is made.

Disabled servers are hidden from key-selection menus, old Telegram buttons are
rejected with a message asking the user to choose another server, and their SSH
connections and background reconnects are skipped. This does not delete server
rows, change remote Xray clients, revoke installed keys, or change hosting bills.
Use stable numeric IDs, not server names; obtain the actual production ID before
setting this variable. Remove an ID and restart to restore its previous behavior.

## X3UI server setup before use

> Instruction is made for Ubuntu 24.04

### Manage new user

#### Add new user

Create new user
```
adduser <username>
```
Add new user to sudo group
```
usermod -aG sudo <username>
```

#### Setup ssh 
Login as new user
```
su - <username>
```
Create .ssh directory
```
mkdir ~/.ssh && chmod 700 ~/.ssh
```

> Create new ssh_key if needed 
> ```
> ssh-keygen -t rsa -b 4096
> ```
> It will be the key that this application will use to connect to api

Add your public key to the authorized_keys file
```
vim ~/.ssh/authorized_keys
chmod 600 ~/.ssh/authorized_keys
```

### Manage ssh

Generate random port, you will use it instead of 22
```
echo $((20000 + RANDOM % (65535 - 20000 + 1)))
```

Change port in ssh.socket. Edit `ListenStream=<new_port>`
```
sudo vim /lib/systemd/system/ssh.socket
```

Edit ssh configuration
```
sudo vim /etc/ssh/sshd_config
```
Find, uncomment or/and edit:
1. `Port <new_port>`
2. `PermitRootLogin no`
3. `PasswordAuthentication no`

Reload system-daemon and sshd
```
sudo systemctl daemon-reload
sudo systemctl restart ssh
```

Make sure ssh uses new port
```
sudo systemctl status ssh
```

Make sure you can connect via ssh using new user, ssh-key and new ssh port
```
ssh -i ~/.ssh/<ssh_key> -p <new_port> <new_user>@<server_ip>
```

### Enable firewall
```
sudo ufw allow <new_port>/tcp
sudo ufw allow 443
sudo ufw enable
```

### Install docker
```
sudo apt update && sudo apt upgrade -y
sudo apt install docker-* -y
```

### Install and setup x3ui
```
sudo apt install git -y
cd /srv
sudo git clone https://github.com/MHSanaei/3x-ui.git
cd 3x-ui
sudo docker-compose up -d
```

Create private.key and public.key for https connection
```
sudo openssl req -x509 -newkey rsa:4096 -nodes -sha256 -keyout private.key -out public.key -days 3650 -subj "/CN=APP"
sudo docker cp private.key 3x-ui:private.key
sudo docker cp public.key 3x-ui:public.key
```

Now you have 3xui running inside docker container. You can access panel using ssh port forwarding
```
ssh -i ~/.ssh/<ssh_key>  -L 2053:localhost:2053 -p <new_port> <new_user>@<server_ip>
```
Type in browser http://localhost:2053 and login to panel with login: `admin`, password: `admin`

1. Go to panel settings 
   1. Set up public key and private key path to `/public.key` and `/private.key` respectively
   2. Change Time Zone to Europe/Moscow or whatever you want.
   3. Save and restart panel
2. Setup TelegramBot (optional)


Then add this server to telegram bot with command `/add_server`

## Security deployment prerequisites

Provision `known_hosts` in the working directory using independently verified SSH host keys before rollout. Unknown and changed host keys now fail closed. The bot accepts account and credential operations only in private chats belonging to the Telegram sender. Command arguments are redacted in notifications and logs. Rotate panel passwords previously submitted with `/add_server`, and remove old sensitive logs/messages through the approved retention workflow. The vendored go-x3ui client omits credential-bearing response bodies and inbound objects from logs.

## Optional managed subscriptions

The main menu keeps three actions: get VPN access, installation, and how it works.
The access screen offers one Otvali subscription containing all managed routes,
plus a separate keys submenu. Managed standalone keys are exported from that same
account and subscription; choosing a key never creates an additional identity.
The legacy Finland key remains available in the keys submenu. Finland's server
records, SSH/3x-ui issuance and existing credentials are preserved. Never add a Remnawave node to the legacy
`servers` table: those rows are interpreted as SSH/3x-ui servers.

`/help_setup` opens the short three-step Russian installation guide without
issuing credentials. Platform instructions and optional client routing sit under
installation. Set optional `VPN_HELP_URL` to the public HTTPS guide URL (without
credentials, query or fragment); platform links add `#windows`, `#macos`,
`#android`, `#ios`, `#linux`, and routing uses `#routing`. With no URL configured,
platform help links to the official Happ and INCY downloads. The public guide
never receives subscription links or server keys.

The feature is disabled by default. Configure the variables in `.env.example`
through OpenBao and the private deployment environment. `REMNA_ALLOWED_TELEGRAM_IDS`
is an explicit snapshot of approved numeric Telegram IDs; for this rollout it
includes every currently bound account. New username-only invitations do not
automatically join that cohort. The user must still have a bound account in
the bot database and make the request in their own private chat.

The API credential needs only `users:create` and `users:by-username`. Production
also restricts its API paths to the bot host's source IP. Neither the operator
admin boundary nor public subscription paths need to be widened. Keep the token,
subscription links and raw API bodies out of logs and administrative messages.

Each Telegram ID has a separate immutable provider mapping. First issuance
creates access with **unlimited traffic and no automatic expiry** by default
(`REMNA_ACCESS_DAYS=0`, including when unset). Remnawave requires an expiration
timestamp; the bot uses `2099-01-01T00:00:00Z`, which the pinned backend
[recognizes as no expiry](https://github.com/remnawave/backend/blob/3.4.5/src/modules/subscription/utils/get-user-info.headers.ts)
and exposes as `expire=0`, with `trafficLimitBytes=0` for unlimited traffic.
The bot shows “Безлимитный трафик • Без автоматического окончания” instead of 2099.
Explicit `REMNA_ACCESS_DAYS=1..3660` retains finite-duration support.
Repeated requests retrieve the same current subscription without extending
expiry, converting an existing finite account to unlimited, or reactivating disabled
accounts. Timeouts are reconciled by a deterministic remote username. Local
mapping failure can be retried without creating another remote account.

Manage existing account policy and revocation through Remnawave; changing the
default does not rewrite already-created accounts. Disable access to reject saved
credentials; rotating only a subscription URL is not a substitute for revoking
downloaded VPN credentials. `/delete_user` refuses to remove a bot account
while its Stockholm grant remains active or cannot be verified. Existing
connections may persist until reconnect; the node does not have connection-drop
capabilities enabled.

Rollback new delivery with `REMNA_ENABLED=false`, retaining the mapping table
and provider revocations. This does not revoke already downloaded Stockholm
credentials. Keep Finland available during rollback.

An eventual Finland import must preserve hostname, port, each VLESS UUID,
REALITY keys/short IDs/SNI and flow. Its current 85 Vision and three empty-flow
credentials need compatibility qualification before changing the live listener.
Do not infer Telegram ownership from a matching historical username.

## Optional Telegram Mini App API

`MINIAPP_ENABLED=false` leaves the HTTP listener closed. Enable only behind the
approved HTTPS reverse proxy, using private container `MINIAPP_PORT=8080` and
exact `MINIAPP_ALLOWED_ORIGINS` from `.env.example`. Do not publish a Docker host
port. `GET /health` checks PostgreSQL without depending on Remnawave; the bot's
existing silent heartbeat health check remains unchanged.

The frontend sends `Authorization: tma <Telegram.WebApp.initData>` and
`Content-Type: application/json` on every private POST. The backend verifies
Telegram's [official bot-token HMAC contract](https://core.telegram.org/bots/webapps#validating-data-received-via-the-mini-app),
including all signed fields except `hash`, and accepts `auth_date` for at most
one hour with a 30-second future clock allowance. That covers importing a
subscription in another app and returning for a key. Reopen the Mini App when
that launch data expires. Use a menu/inline web-app button or main Mini App;
reply-keyboard launch does not supply the required signed user data. No cookie,
local login account or frontend-selected Telegram ID is accepted. Origins are
an additional restriction, never a replacement for authentication.

When enabled, the bot adds an inline **Открыть приложение** button above its
existing three main actions. It uses `VPN_HELP_URL` only when that URL's exact
HTTPS origin is in `MINIAPP_ALLOWED_ORIGINS`; an empty URL leaves the launch
button hidden. The Telegram subscription/key flow remains available.

| Endpoint | JSON request | Success response |
|---|---|---|
| `POST /api/miniapp/session` | `{}` | `{"account":{"managed":true,"legacy_finland":true}}` |
| `POST /api/miniapp/subscription` | `{}` | `{"subscription":{"url":"…","expires_at":null,"traffic_limit_bytes":0}}` |
| `POST /api/miniapp/keys` | `{}` | `{"keys":[{"id":"opaque16hex","name":"…"},{"id":"legacy_fi","name":"Finland, Helsinki"}]}` |
| `POST /api/miniapp/key` | `{"id":"opaque16hex"}` or `{"id":"legacy_fi"}` | `{"key":{"id":"…","name":"…","url":"vless://…"}}` |

Session checks are read-only and never create grants. Additive `managed_state`
and `legacy_finland_state` fields distinguish `ready`, `eligible_unprovisioned`
(managed only), `access_disabled`, `not_found`, `unavailable`, and `not_eligible`.
The existing booleans remain compatible: managed access is offered when ready
or explicitly eligible but unprovisioned; known inactive accounts are false.
Legacy Finland is true only after a current numeric-owned active client check.
Each session response also contains `checked_at` (RFC3339 UTC) and
`check_scope: "account_access"`. Managed and legacy provider reads run
independently, each with an eight-second deadline, so one outage does not erase
the other provider's current readiness. This checks account authorization and
provider availability; it does not test a customer's device, route, speed or
VPN traffic. Neither check retrieves keys or creates an account.

The private bot command `/status` and **Проверить доступ** action under
**Как это работает** use this same read-only service. They show both account
states with refresh, installation and access navigation. Refreshing status is
never an issuance action. Existing-only Finland ownership and disabled-account
rules apply unchanged.

Issuance/listing actions
require an explicit frontend action. Managed actions use the same bot service,
immutable grant mapping and current approved numeric cohort; listing exposes
names/opaque IDs only, and selecting a key fetches that same account's current
profiles. No automatic signup, invitation binding, expiry extension or disabled
account reactivation occurs. Legacy Finland checks the existing bound numeric
user, server policy and exclusive role; ownership is proved by the panel's
numeric Telegram ID. An unbound historical credential cannot be claimed by a
matching username. Its existing UUID/email remain unchanged, and a shared lock serializes
Mini App reads with the bot's existing issuance path. Mini App Finland is
**existing-only**: missing, deleted or never-provisioned clients are never
created or recreated by session, listing or retrieval. Legacy bot provisioning
retains its existing behavior. Known disabled/expired/deleted/conflicting
managed access is read without renewal and reported separately from outages.

All private responses and errors use `Cache-Control: no-store, private`.
Requests have bounded headers/bodies, strict fields, timeouts, eight in-flight
slots, 20 requests per numeric account per minute and 300 globally per minute.
Errors contain only `{"error":{"code":"…"}}` with `unauthorized`, `forbidden`,
`invalid_request`, `rate_limited`, `access_disabled`, `unavailable`, `not_found`, or
`method_not_allowed`; credentials and raw provider errors are never logged.
The frontend must keep initData/keys/subscriptions in memory only, render them
as text, and send none in query strings, analytics, storage or error reports.
A managed-provider failure leaves the legacy Finland action independent.

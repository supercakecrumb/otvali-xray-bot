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

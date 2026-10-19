package telegram

const (
	clientDownloadsHTML = `Choose <a href="https://happ.info/">Happ: official downloads</a> or <a href="https://github.com/INCY-DEV/incy-platforms">INCY: official downloads</a>. Follow the official link for a build that supports your device.`
	clientImportHTML    = `<b>Import the right kind of access:</b>
• <b>Finland server key:</b> copy the complete <code>vless://</code> key and import it from the clipboard as a <b>server</b>. It is one server, not a subscription URL.
• <b>Otvali subscription:</b> copy the complete <code>https://</code> subscription URL and add it as a <b>subscription</b>. Refresh it, select an available profile and connect. Refresh this same URL to receive new profiles.

App control names vary. Keep your existing Finland profile as a fallback. For optional RU-direct routing and your own exceptions, use the routing guide when it is available below.

<b>Keep keys and subscription URLs private.</b> Both grant VPN access; do not forward or post them.`
	InstructionLinux = "<b>VPN setup on Linux with Happ or INCY</b>\n\n" + clientDownloadsHTML +
		"\n\nInstall the official package for your Linux distribution and CPU architecture. Open the app and approve any required network permissions.\n\n" + clientImportHTML
	InstructionWindows = "<b>VPN setup on Windows with Happ or INCY</b>\n\n" + clientDownloadsHTML +
		"\n\nInstall the official Windows build for your device. Open the app and approve any required network permissions.\n\n" + clientImportHTML
	InstructionAndroid = "<b>VPN setup on Android with Happ or INCY</b>\n\n" + clientDownloadsHTML +
		"\n\nUse the store or Android release linked by the official project. Open the app; approve Android’s system VPN permission when you first connect.\n\n" + clientImportHTML +
		"\n\n<b>Android TUN / VPN permission denied:</b> disconnect other VPN apps. In Android’s VPN settings, check whether Always-on VPN or Block connections without VPN is assigned to another app. If you intend to switch apps, update that selection, then connect Happ or INCY and approve the system VPN prompt. This local VPN-interface error happens before the server connection; a new server key will not fix that permission."
	InstructionIOS = "<b>VPN setup on iOS with Happ or INCY</b>\n\n" + clientDownloadsHTML +
		"\n\nUse the App Store link provided by the official project for your region. Open the app; approve iOS’s system VPN permission when you first connect.\n\n" + clientImportHTML
	InstructionMacOS = "<b>VPN setup on macOS with Happ or INCY</b>\n\n" + clientDownloadsHTML +
		"\n\nInstall the official macOS build for your device. Open the app and approve any required VPN/network permissions.\n\n" + clientImportHTML

	howItWorksText = `<b>How Otvali VPN works</b>

Access is invite-only. An existing member or administrator can use <code>/invite @username</code> to invite someone they trust. Use <code>/get_key</code> to see your available choices.

<b>Server key or subscription?</b>
A legacy Finland <code>vless://</code> key is a single server configuration. An Otvali <code>https://</code> subscription URL is a refreshable list of available profiles. Import each using the matching option in Happ or INCY. Keep an existing working Finland profile as a fallback.

<b>Routing</b>
Optional client routing can send Russian destinations directly through your own Internet connection. Moscow profiles also apply their own server-side RU-direct policy. A Russian site that needs a foreign IP requires a direct foreign profile and a Proxy exception. Setup and routing guides appear for eligible accounts.

<b>Privacy and support</b>
Keys and subscription URLs grant access; keep them private. Send the bot a message to contact the administrators, without including your key or subscription URL.`
)

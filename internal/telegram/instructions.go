package telegram

const (
	platformImportText = "\n\n1. Установите Happ или INCY.\n2. Добавьте подписку как подписку, а vless:// ключ — как сервер.\n3. Выберите маршрут, разрешите VPN и подключитесь в приложении.\n\nПодробная инструкция — по кнопке ниже."
	InstructionLinux   = "<b>Linux</b>" + platformImportText
	InstructionWindows = "<b>Windows</b>" + platformImportText
	InstructionAndroid = "<b>Android</b>" + platformImportText + "\n\nОшибка TUN / permission denied: отключите другой VPN, проверьте Always-on VPN / блокировку без VPN в настройках Android и разрешите VPN для выбранного приложения."
	InstructionIOS     = "<b>iOS</b>" + platformImportText
	InstructionMacOS   = "<b>macOS</b>" + platformImportText
	howItWorksText     = "<b>Как это работает</b>\n\nОдна подписка — все новые маршруты. Обновляйте её в Happ или INCY.\n\nОтдельный ключ добавляется как сервер. Прежний ключ Финляндии не меняется.\n\nVPN включите в приложении."
)

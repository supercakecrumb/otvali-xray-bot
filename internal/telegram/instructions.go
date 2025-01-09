package telegram

var (
	InstructionHiddifyLinux = `<b>Установка Hiddify на Linux:</b>

1. Рекомендуется обновить пакеты на вашей системе (например, в Debian/Ubuntu):
<pre><code>sudo apt update && sudo apt upgrade -y
</code></pre>

2. Скачайте и запустите скрипт установки Hiddify:
<pre><code>curl -fsSL https://github.com/hiddify/hiddify-config/raw/main/install.sh | bash
</code></pre>

3. Следуйте инструкциям, которые появятся в терминале.

4. <b>Использование ключа:</b>
   - Получите ключ через команду /get_key или кнопку в меню бота.
   - Импортируйте полученный ключ в настройки Hiddify.`

	InstructionHiddifyWindows = `<b>Установка Hiddify на Windows:</b>

1. Скачайте установочный файл Hiddify для Windows с <a href="https://github.com/hiddify/hiddify-config">официальной страницы проекта</a> или используйте подготовленный релиз (если доступен).

2. Установите программу, следуя инструкциям мастера установки.

3. <b>Использование ключа:</b>
   - Получите ключ через команду /get_key или кнопку в меню бота.
   - Импортируйте полученный ключ в настройки Hiddify.`

	InstructionHiddifyAndroid = `<b>Установка Hiddify на Android:</b>

1. Установите приложение Hiddify:
   - <a href="https://play.google.com/store/apps/details?id=app.hiddify.com&pcampaignid=web_share">Ссылка на Google Play</a> (если приложение доступно в Google Play)
   - или загрузите APK-файл с <a href="https://github.com/hiddify/hiddify-config">официального репозитория GitHub</a> и установите вручную.

2. Запустите приложение и выполните первичную настройку.

3. <b>Использование ключа:</b>
   - Получите ключ через команду /get_key или кнопку в меню бота.
   - Импортируйте полученный ключ в настройки Hiddify (обычно это меню “Import Key” или аналогичное).`

	InstructionHiddifyIOS = `<b>Установка Hiddify на iOS:</b>

1. Установите приложение Hiddify:
   - <a href="https://apps.apple.com/ru/app/hiddify-proxy-vpn/id6596777532?l=en-GB">Ссылка на App Store</a> (если приложение доступно в App Store)
   - или следуйте инструкциям с <a href="https://github.com/hiddify/hiddify-config">официального репозитория GitHub</a> (если в App Store нет опубликованной версии).

2. Откройте приложение и выполните начальную настройку.

3. <b>Использование ключа:</b>
   - Получите ключ через команду /get_key или кнопку в меню бота.
   - Импортируйте полученный ключ в настройки Hiddify (обычно через пункт “Import Key” или похожий вариант).`

	InstructionHiddifyMacOS = `<b>Установка Hiddify на macOS:</b>

1. Установите <a href="https://brew.sh">Homebrew</a>, если у вас его ещё нет:
<pre><code>/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
</code></pre>

2. Установите Hiddify через Homebrew:
<pre><code>brew install hiddify
</code></pre>

3. <b>Использование ключа:</b>
   - Получите ключ через команду /get_key или кнопку в меню бота.
   - Откройте настройки Hiddify и импортируйте полученный ключ.`
)

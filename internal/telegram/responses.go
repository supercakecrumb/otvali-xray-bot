package telegram

// CommandResponses stores the predefined responses for specific commands
var CommandResponses = map[string]string{
	"/start": "Welcome to the bot! Use /help to see available commands.",
	"/help":  "Here are the available commands:\n/start - Start the bot\n/help - Show this help message",
}

var noUsernameResponse = "Ты не можешь пользоваться ботом пока у тебя нет имени пользователя. [Как это сделать](https://tinyurl.com/4hjse9w4)"

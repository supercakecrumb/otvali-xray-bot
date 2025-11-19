package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Println("Testing environment variables:")
	fmt.Println("TELEGRAM_TOKEN:", os.Getenv("TELEGRAM_TOKEN"))
	fmt.Println("LOG_LEVEL:", os.Getenv("LOG_LEVEL"))
	fmt.Println("DATABASE_URL:", os.Getenv("DATABASE_URL"))
	fmt.Println("SSH_KEY_PATH:", os.Getenv("SSH_KEY_PATH"))

	if os.Getenv("TELEGRAM_TOKEN") == "" {
		fmt.Println("WARNING: TELEGRAM_TOKEN is not set")
	}
	if os.Getenv("DATABASE_URL") == "" {
		fmt.Println("WARNING: DATABASE_URL is not set")
	}

	fmt.Println("\nIf you see this message, basic output is working.")
}

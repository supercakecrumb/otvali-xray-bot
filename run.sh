#!/bin/bash

# Set environment variables
export TELEGRAM_TOKEN="your_telegram_token_here"
export LOG_LEVEL="debug"  # Set to debug for maximum logging
export DATABASE_URL="your_database_url_here"  # e.g., sqlite:///data.db or postgres://user:pass@localhost:5432/dbname
export SSH_KEY_PATH="your_ssh_key_path_here"  # e.g., ~/.ssh/id_rsa

# Run the application with verbose output
echo "Starting application with environment variables:"
echo "TELEGRAM_TOKEN: [set]"
echo "LOG_LEVEL: $LOG_LEVEL"
echo "DATABASE_URL: $DATABASE_URL"
echo "SSH_KEY_PATH: $SSH_KEY_PATH"
echo ""

# Run the application
go run cmd/main.go
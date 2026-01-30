#!/bin/bash
BLUE='\033[0;34m'
NC='\033[0m'

messages=(
    "INFO: Processing batch job #$RANDOM"
    "DEBUG: Cache hit ratio: $((RANDOM % 100))%"
    "INFO: New connection from 192.168.1.$((RANDOM % 255))"
    "DEBUG: Query executed in $((RANDOM % 500))ms"
    "INFO: User session started: user_$((RANDOM % 1000))"
    "DEBUG: Garbage collection completed"
    "INFO: API request: GET /api/v1/users"
    "DEBUG: WebSocket ping/pong successful"
    "INFO: Background task completed successfully"
    "DEBUG: Loading configuration from env"
)

i=0
while true; do
    ((i++))
    msg=${messages[$RANDOM % ${#messages[@]}]}
    echo -e "${BLUE}[$i] $msg${NC}"
    sleep $(awk -v min=0.5 -v max=2.5 'BEGIN{srand(); print min+rand()*(max-min)}')
done

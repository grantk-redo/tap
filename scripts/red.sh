#!/bin/bash
trap 'exit 0' SIGINT SIGTERM

RED='\033[0;31m'
NC='\033[0m'

messages=(
    "ERROR: Connection refused to database"
    "WARN: Retry attempt failed, backing off"
    "ERROR: Timeout waiting for response"
    "CRITICAL: Memory usage at 95%"
    "ERROR: Failed to parse config file"
    "WARN: Certificate expires in 7 days"
    "ERROR: Disk space low on /var/log"
    "FATAL: Unhandled exception in worker thread"
    "ERROR: Authentication failed for user admin"
    "WARN: Rate limit exceeded, throttling"
)

i=0

# Sprint: 60 logs quickly
for j in {1..60}; do
    ((i++))
    msg=${messages[$RANDOM % ${#messages[@]}]}
    echo -e "${RED}[$i] $msg${NC}"
    sleep 0.016  # ~60 per second
done

# Then slow down
while true; do
    ((i++))
    msg=${messages[$RANDOM % ${#messages[@]}]}
    echo -e "${RED}[$i] $msg${NC}"
    sleep $(awk -v min=0.5 -v max=2.0 'BEGIN{srand(); print min+rand()*(max-min)}')
done

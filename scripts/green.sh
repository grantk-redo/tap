#!/bin/bash
trap 'exit 0' SIGINT SIGTERM
GREEN='\033[0;32m'
NC='\033[0m'

messages=(
    "OK: Health check passed"
    "SUCCESS: Deployment completed"
    "OK: All tests passing ($(( RANDOM % 50 + 100 )) tests)"
    "SUCCESS: Backup completed successfully"
    "OK: SSL handshake verified"
    "SUCCESS: Data sync finished"
    "OK: Service registered with discovery"
    "SUCCESS: Migration applied: v$((RANDOM % 100))"
    "OK: Metrics exported to Prometheus"
    "SUCCESS: Cache warmed up"
)

i=0
while true; do
    ((i++))
    msg=${messages[$RANDOM % ${#messages[@]}]}
    echo -e "${GREEN}[$i] $msg${NC}"
    sleep $(awk -v min=1.0 -v max=4.0 'BEGIN{srand(); print min+rand()*(max-min)}')
done

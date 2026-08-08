#!/bin/bash
set -e

SERVER="root@104.131.94.68"
REMOTE_DIR="/var/www/pareto"
PORT=8082

GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o /tmp/pareto-bin .

ssh $SERVER "mkdir -p $REMOTE_DIR/data"
scp index.html openapi.json robots.txt sitemap.xml llms.txt llms-full.txt $SERVER:$REMOTE_DIR/
scp aliases.json $SERVER:$REMOTE_DIR/
scp /tmp/pareto-bin $SERVER:/opt/pareto-bin.new
ssh $SERVER "mv /opt/pareto-bin.new /opt/pareto-bin && chmod +x /opt/pareto-bin"

# Ship a schema-matched snapshot with every release. Publish data.js last so
# browsers never observe a new payload before JSON/API readers and the quality
# report can see the same publication.
scp data/quality.json $SERVER:$REMOTE_DIR/data/
scp data/data.json $SERVER:$REMOTE_DIR/data/
scp data/data.js $SERVER:$REMOTE_DIR/data/

# Plumbing (systemd unit, nginx vhost) is owned by ~/repos/infra — this
# script only ships content and the binary.
ssh $SERVER "systemctl restart pareto"

echo "Deployed pareto service to pareto.ehrlich.dev (port $PORT)"

#!/bin/bash
set -e

SERVER="root@104.131.94.68"
REMOTE_DIR="/var/www/pareto"
NGINX_CONF="/etc/nginx/sites-enabled/pareto.ehrlich.dev.conf"
UNIT="/etc/systemd/system/pareto.service"
PORT=8082

GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o /tmp/pareto-bin .

ssh $SERVER "mkdir -p $REMOTE_DIR/data"
scp index.html $SERVER:$REMOTE_DIR/
scp /tmp/pareto-bin $SERVER:/opt/pareto-bin.new
ssh $SERVER "mv /opt/pareto-bin.new /opt/pareto-bin && chmod +x /opt/pareto-bin"

# seed the data snapshot only if the server has none — the app refreshes its own
ssh $SERVER "test -f $REMOTE_DIR/data/data.js" || scp data/data.js $SERVER:$REMOTE_DIR/data/

ssh $SERVER "cat > $UNIT << 'EOF'
[Unit]
Description=pareto.ehrlich.dev
After=network.target

[Service]
Type=simple
ExecStart=/opt/pareto-bin -addr 127.0.0.1:$PORT -root $REMOTE_DIR -every 6h
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload && systemctl enable --now pareto && systemctl restart pareto"

ssh $SERVER "cat > $NGINX_CONF << 'EOF'
server {
    listen 80;
    listen [::]:80;

    server_name pareto.ehrlich.dev;

    location / {
        proxy_pass http://127.0.0.1:$PORT;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
    }
}
EOF
nginx -t && nginx -s reload"

echo "Deployed pareto service to pareto.ehrlich.dev (port $PORT)"

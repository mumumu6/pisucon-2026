# サーバー初期配布の env.sh（例年ここにある。systemd の EnvironmentFile が読む）。
# Ansible は MYSQL_HOST= の行だけ inventory に合わせて書き換える（無い場合は作らない）。
MYSQL_HOST="127.0.0.1"
MYSQL_PORT=3306
MYSQL_USER=isucon
MYSQL_DBNAME=isucondition
MYSQL_PASS=isucon
POST_ISUCONDITION_TARGET_BASE_URL="http://isucondition-1.t.isucon.dev"

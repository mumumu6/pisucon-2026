# サーバー初期配布の env.sh（systemd の EnvironmentFile が読む）。
# Ansible は触らない。DB を別ホストにするときだけ MYSQL_HOST を手で直す。
MYSQL_HOST="10.0.0.157"
MYSQL_PORT=3306
MYSQL_USER=isucon
MYSQL_DBNAME=isucondition
MYSQL_PASS=isucon
POST_ISUCONDITION_TARGET_BASE_URL="http://isucondition-1.t.isucon.dev"

# サーバー初期配布の env.sh（大会固有の値が入っている想定）。
# Ansible は MYSQL_HOST= の行だけ inventory に合わせて書き換える（他行は触らない）。
MYSQL_HOST="127.0.0.1"
MYSQL_PORT=3306
MYSQL_USER=isucon
MYSQL_DBNAME=isucondition
MYSQL_PASS=isucon
POST_ISUCONDITION_TARGET_BASE_URL="http://isucondition-1.t.isucon.dev"

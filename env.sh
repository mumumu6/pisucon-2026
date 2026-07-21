# サーバー初期配布の env.sh（例年ここにある。systemd の EnvironmentFile が読む）。
# あれば Ansible は MYSQL_HOST= だけ合わせる。無ければ最小生成する（イレギュラー対応）。
MYSQL_HOST="127.0.0.1"
MYSQL_PORT=3306
MYSQL_USER=isucon
MYSQL_DBNAME=isucondition
MYSQL_PASS=isucon
POST_ISUCONDITION_TARGET_BASE_URL="http://isucondition-1.t.isucon.dev"

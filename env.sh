# このファイルは Ansible（tasks/app/env.yml）がサーバー上で管理する。
# 手元の編集は templates/env.sh.j2 / group_vars の mysql_*・app_env_extra を正とする。
# MYSQL_HOST は inventory の db.private_ip（同居なら 127.0.0.1）から自動設定される。
MYSQL_HOST="10.0.0.121"
MYSQL_PORT=3306
MYSQL_USER=isucon
MYSQL_DBNAME=isucondition
MYSQL_PASS=isucon
POST_ISUCONDITION_TARGET_BASE_URL="http://isucondition-1.t.isucon.dev"

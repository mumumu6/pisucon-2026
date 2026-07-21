# config-pull が server-config へ書き足す配線・チューニング候補

初回 `make bootstrap`（config-pull）だけが読む。ここを直すと、次に新規取り込みする大会の初期追記内容が変わる。
すでに取り込まれた `server-config/` は上書きしない。

| ファイル | 挿入先 |
| --- | --- |
| `nginx-main.conf` | `nginx.conf` の `events {` 直前 |
| `nginx-events.conf` | `events {` 直後 |
| `nginx-http.conf.j2` | `http {` 直後（ltsv + コメントアウトのチューニング） |
| `mysql.cnf.j2` | `mysql_server_cnf` の basename（例: 50-server.cnf） 末尾（slow log 配線 + チューニング候補） |

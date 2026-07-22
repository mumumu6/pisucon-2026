# isucon-bench

ISUCON 素振り用の Ansible + Make ツールキット。

開始直後に手で書く項目（サービス名・alp・IP など）はリポジトリ直下の [README.md](../../README.md) の「まず初めにやること」を参照。

## 本番（tools だけ持参）

1. 空の private repo を作り、`Makefile` + `tools/isucon-bench/` だけ置く
2. `inventory.yml` / `group_vars/all.yml` を大会用に書く（`app_name` / `alp_matching_groups` / `git_repository`。言語は Go 固定）
3. `files/github_id_ed25519[.pub]` を置く
4. `make bootstrap`
   - `server-config/` … nginx / MariaDB など etc
   - `webapp/go` / `sql` / `frontend` / `env.sh` … rsync（`project_seed_paths`）
   - どちらも commit / push 済み → 以後 `make deploy` で全ホストへ

手元にすでに `webapp/go` がある（素振り monorepo など）ときは webapp 取り込みはスキップする。
取り直すときは `webapp/go`（と必要なら `webapp/sql` / `webapp/frontend` / `env.sh`）を消してから `make config-pull`。

## 別リポジトリ / 別大会で使うとき

1. このディレクトリとルートの `Makefile` を持っていく（またはこの repo をベースにする）
2. `inventory.yml` / `group_vars/all.yml` を大会用に書き換える
3. `files/github_id_ed25519[.pub]` を置く（gitignore 済み。詳細は `ansible/files/README.md`）
4. `server-config/` が残っていれば削除してから `make bootstrap`

## 初回追記の中身

`ansible/files/wiring/` を編集する（`config-pull.yml` への直書きではない）。

## 注意（大会イメージ依存）

- SSL が有効な nginx サイトはそのまま取り込む。無効証明書だとベンチ不可なので、取り込み後に `server-config/sites/` を手で HTTP 化する（問題の注意書きに従う）
- DB cnf のパスは `mysql_server_cnf`（手元ファイル名は basename 自動）
- サイト設定ディレクトリは `nginx_sites_dir`（既定 `/etc/nginx/sites-enabled`）
- systemd drop-in は**サーバーにファイルがあるときだけ**取り込む。無い大会では作らない（後から `server-config/systemd/` に追加して deploy すればよい）
- `project_seed_paths` に無いホーム配下（`go/pkg` や `.ssh` など）は取り込まない

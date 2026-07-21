# pisucon-2026 / ISUCON 素振り Ansible

ISUCONサーバーの再構築、ベンチ前後の計測、結果回収を Ansible + Make で自動化します。
**大会固有の値は `group_vars/all.yml` 先頭の「大会ごとに書き換える設定」に集約**しているので、
別回の ISUCON でも変数を書き換えて使い回せます。

サーバーの設定（nginx / MariaDB）は `make bootstrap` が **repo の `server-config/` に取り込んで git 管理**します。
以後はその実ファイルを直接編集して `make deploy` / `make mysql-tune` で反映します（テンプレートはありません）。
初回に足す配線・チューニング候補の中身は [`ansible/files/wiring/`](tools/isucon-bench/ansible/files/wiring/) を編集する。

## 手元PCの前提ツール

```bash
sudo apt update
sudo apt install -y ansible jq git gh
```

## 別回の ISUCON で使うとき

1. `tools/isucon-bench/ansible/inventory.yml` … 接続先 IP と `app` / `db` / `nginx` / `reporter`
2. `tools/isucon-bench/ansible/group_vars/all.yml` 先頭 … `app_name` / パス / Git / ビルドコマンド
3. 前回の `server-config/` が残っていれば削除（bootstrap が新しいサーバーから取り込み直す）
4. **GitHub 用 SSH 鍵（デプロイ鍵）**を `tools/isucon-bench/ansible/files/github_id_ed25519[.pub]` に配置（gitignore 済み）  
   → サーバーから private リポジトリを pull するための鍵。**チームで1組あればよい**（誰か1人の鍵でOK）。詳細は `tools/isucon-bench/ansible/files/README.md`
5. ツールキット単体の説明は [`tools/isucon-bench/README.md`](tools/isucon-bench/README.md)

```bash
make bootstrap
make bench
```

## server-config/ の運用（チューニングの反映）

`make bootstrap` が次を自動で行う:

1. サーバーの素の設定を `server-config/` へ取り込み、commit（サーバー側には `.orig` を残す）
2. [`files/wiring/`](tools/isucon-bench/ansible/files/wiring/) の配線（ltsv / slow log）と
   コメントアウトされたチューニング候補を書き足して commit、push
   （追記内容を変えたいときはそのディレクトリを編集）

以後の流れ:

```bash
$EDITOR server-config/nginx.conf      # 効かせたい行のコメントを外す
make deploy                            # git sync + appビルド/再起動 + nginx設定反映

$EDITOR server-config/50-server.cnf   # 例: innodb_buffer_pool_size を有効化
make mysql-tune                        # MariaDB設定反映（変更時だけ再起動）

make bench                             # スコアを確認して効いたら git commit
```

- 取り込んだファイル: `server-config/nginx.conf`（→ `/etc/nginx/nginx.conf`）、
  `server-config/sites/*`（→ `/etc/nginx/sites-enabled/`）、
  `server-config/50-server.cnf`（→ `/etc/mysql/mariadb.conf.d/50-server.cnf`）、
  `server-config/systemd/*.override.conf`（→ 各 systemd drop-in。サーバーに無ければ取り込まない）
- 素の状態は git の最初の commit で参照・巻き戻しできる
- app の GOGC / GOMEMLIMIT や unix socket 化は
  `server-config/systemd/app.override.conf` を編集して `make deploy`
- ログのパス（`access_log` / slow log）を変える場合は `group_vars/all.yml` も合わせる
  （bench の truncate / 解析が同じパスを見る）

### ホストを分離するとき（手で直す箇所）

| 箇所 | 直す場所 |
| --- | --- |
| nginx → app | `server-config/` の proxy_pass / upstream を app の `private_ip:3000` に |
| app の listen | `server-config/systemd/app.override.conf` の `SERVER_APP_PORT` を有効化 |
| app → db | サーバー上の `~/env.sh` の `MYSQL_HOST` を db の `private_ip` に（Ansible は触らない） |

MariaDB は配線で `bind-address=0.0.0.0` + リモート GRANT 済みなので、DB 側の追加作業は不要。

## クイックスタート

```bash
$EDITOR tools/isucon-bench/ansible/inventory.yml
$EDITOR tools/isucon-bench/ansible/group_vars/all.yml
# デプロイ鍵を files/ へ
make bootstrap
make bench
```

| ファイル | 内容 |
| --- | --- |
| `inventory.yml` | 接続先・役割 |
| `group_vars/all.yml` | 大会変数 + 計測/DB の共通設定 |
| `server-config/` | サーバー設定の実体（bootstrap が取り込み、git 管理） |
| `Makefile` | 運用コマンドの入口（全部ここから） |

## 普段の運用

```bash
make help
make server-sync     # 全サーバーへ git sync
make deploy          # server-sync + systemd + ビルド + app/nginx(server-config) 反映
make mysql-tune      # server-config/50-server.cnf を反映
make bench           # 計測・解析・回収
make bench PUBLISH=true
make finish          # 本気計測前に計測負荷を外す
make get-log-detail  # 詳細ログを手元へ（次の bench 前に）
make collect SESSION=20260719-123000
```

`make bench` の流れ:

1. server-sync → app ビルド・再起動、nginx / MariaDB 再起動
2. ログ初期化、pprof 武装
3. ブラウザで公式ベンチ開始を待つ → スコア入力
4. alp / pt-query-digest / pprof 解析
5. `log/<SESSION>/` へ回収、`REPORT.md` 生成

## Make ターゲット一覧

| ターゲット | 用途 |
| --- | --- |
| `bootstrap` | 設定取り込み(git) + ツール導入 + 計測系 ON |
| `config-pull` | サーバー設定を `server-config/` へ取り込み commit/push（bootstrap に含まれる） |
| `server-sync` | GitHub 指定ブランチを全サーバーへ同期 |
| `deploy` | server-sync + ビルド + app/nginx 設定反映 |
| `fleet-enable` / `fleet-disable` | 計測系 ON/OFF |
| `finish` | `fleet-disable` と同じ |
| `mysql-tune` | `server-config/50-server.cnf` を反映 |
| `collect` | ベンチ結果を手元へ再取得 |
| `get-log-detail` | nginx/DB/Go の詳細ログを手元へ（次の bench 前に） |
| `pprof-view` / `netdata-view` | 手元でプロファイル / Netdata を見る |
| `restart` | 全ホスト OS 再起動（追試用） |

## 構成

```text
Makefile
server-config/                      # サーバー設定の実体（bootstrap が取り込み、git 管理）
└── tools/isucon-bench/
    ├── ansible/
    │   ├── ansible.cfg
    │   ├── inventory.yml
    │   ├── group_vars/all.yml      # 大会変数は先頭セクション
    │   ├── config-pull.yml / setup.yml / deploy.yml / bench.yml / git.yml / monitor.yml / …
    │   ├── tasks/
    │   │   ├── common/   # packages, github-ssh, services-restart
    │   │   ├── app/      # packages, systemd, build
    │   │   ├── nginx/    # packages(+alp), configure
    │   │   ├── db/       # packages, performance
    │   │   └── bench/    # prepare, measure, analyze
    │   ├── templates/    # pprof / fleet-report / SSH config など（設定本体は server-config/）
    │   └── files/        # GitHub SSH 鍵（gitignore）
    └── scripts/
```

入口はルートの playbook、再利用ロジックは `tasks/` に置きます。
アプリのビルド＋再起動は常に `tasks/app/build.yml`、サービス再起動は常に
`tasks/common/services-restart.yml` を使います。

## Ansible の確認

```bash
export ANSIBLE_CONFIG=tools/isucon-bench/ansible/ansible.cfg
ansible-inventory -i tools/isucon-bench/ansible/inventory.yml --graph
ansible all -i tools/isucon-bench/ansible/inventory.yml -m ping
```

`bench.yml` はセッション ID を Makefile が渡すため、直接実行せず `make bench` を使います。

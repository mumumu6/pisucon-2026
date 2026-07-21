# pisucon-2026 / ISUCON 素振り Ansible

ISUCONサーバーの再構築、ベンチ前後の計測、結果回収を Ansible + Make で自動化します。
**大会固有の値は `group_vars/all.yml` 先頭の「大会ごとに書き換える設定」に集約**しているので、
別回の ISUCON でも変数（と必要なら nginx テンプレート）を書き換えて使い回せます。

## 手元PCの前提ツール

```bash
sudo apt update
sudo apt install -y ansible jq git gh
```

## 別回の ISUCON で使うとき

1. `tools/isucon-bench/ansible/inventory.yml` … 接続先 IP と `app` / `db` / `nginx` / `reporter`
2. `tools/isucon-bench/ansible/group_vars/all.yml` 先頭 … `app_name` / パス / Git / ビルドコマンド
3. （必要なら）`templates/nginx.conf.j2` や `mysql-performance.cnf.j2` を直接編集
4. **GitHub 用 SSH 鍵（デプロイ鍵）**を `tools/isucon-bench/ansible/files/github_id_ed25519[.pub]` に配置（gitignore 済み）  
   → サーバーから private リポジトリを pull するための鍵。**チームで1組あればよい**（誰か1人の鍵でOK）。詳細は `tools/isucon-bench/ansible/files/README.md`
5. **DB を別ホストにするとき** … サーバー上の `~/env.sh` の `MYSQL_HOST` を **手で** db の `private_ip` に直す（Ansible は触らない）

役割は `inventory.yml` のグループと `private_ip` で決まる。nginx↔app の接続先は Ansible が自動で埋める:

| 箇所 | 同居 | 分離 |
| --- | --- | --- |
| nginx → app | unix socket | `private_ip:app_listen_port`（app 複数なら全部） |
| app listen | `SERVER_APP_SOCK` | `SERVER_APP_PORT` |
| `env.sh` の `MYSQL_HOST` | `127.0.0.1` のまま | **手で** db の `private_ip` に変更 |

MariaDB は構成によらず `bind-address=0.0.0.0` + リモート GRANT。  
`env.sh` は例年どおり systemd の `EnvironmentFile` が読む。Ansible では書き換えないので、DB 分離時は必ず手で直す。

```bash
make bootstrap
make bench
```

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
| `Makefile` | 運用コマンドの入口（全部ここから） |

## 普段の運用

```bash
make help
make server-sync     # 全サーバーへ git sync
make build           # server-sync + systemd + ビルド + app/nginx 再起動
make pull            # 最新ベンチ結果 + 設定バックアップを手元へ
make bench           # 計測・解析・回収
make bench PUBLISH=true
make finish          # 本気計測前に計測負荷を外す
make collect SESSION=20260719-123000
```

`make bench` の流れ:

1. server-sync → app ビルド・再起動、nginx / MariaDB 再起動
2. ログ初期化、pprof 武装
3. ブラウザで公式ベンチ開始を待つ → スコア入力
4. alp / pt-query-digest / pprof 解析（メトリクス・ログは Grafana）
5. `log/<SESSION>/` へ回収、`REPORT.md` 生成

## Make ターゲット一覧

| ターゲット | 用途 |
| --- | --- |
| `bootstrap` | ツール導入 + git + 計測系 ON + `collect-backups` |
| `server-sync` | GitHub 指定ブランチを全サーバーへ同期 |
| `pull` | 最新ベンチ結果 + `collect-backups` を手元へ取得 |
| `build` | server-sync + ビルド/再起動 + `collect-backups` |
| `fleet-setup` | 計測ツール導入＋計測系 ON + `collect-backups` |
| `fleet-enable` / `fleet-disable` | 計測系 ON/OFF + `collect-backups` |
| `mysql-tune` | MariaDB 性能 cnf 反映 + `collect-backups` |
| `collect-backups` | 設定バックアップだけ回収 |
| `pprof-view` / `grafana-view` | 手元でプロファイル / Grafana を見る |
| `restart` | 全ホスト OS 再起動（追試用） |
| `finish` | 最終計測前に計測系を外す（=`fleet-disable`） |

観測スタック（`observe`）は reporter に Grafana + Prometheus + Loki、各ホストに node_exporter / Alloy（+ DB に mysqld_exporter）です。`make grafana-view` で `http://localhost:3000` を開きます。

## 構成

```text
Makefile
└── tools/isucon-bench/
    ├── ansible/
    │   ├── ansible.cfg
    │   ├── inventory.yml
    │   ├── group_vars/all.yml      # 大会変数は先頭セクション
    │   ├── setup.yml / build.yml / bench.yml / …  # 薄い playbook（組み立てだけ）
    │   ├── handlers/main.yml
    │   ├── tasks/
    │   │   ├── common/   # packages, github-ssh, git-sync, fleet-services
    │   │   ├── app/      # packages, systemd, build, restart, deploy, pprof
    │   │   ├── nginx/    # packages, alp, configure, site, restart
    │   │   ├── db/       # packages, performance, restart, slow-query
    │   │   ├── observe/  # Grafana/Prometheus/Loki + exporters/Alloy
    │   │   ├── monitor/  # toggle（observe + slow query + pprof）
    │   │   └── bench/    # prepare, measure, analyze-*
    │   ├── templates/
    │   └── files/        # GitHub SSH 鍵（gitignore）
    └── scripts/
```

役割ごと・再利用単位に `tasks/` を分割しています。例えばアプリのビルド＋再起動は
`tasks/app/deploy.yml` にまとめ、`build.yml` と `bench.yml` の両方から使います。
systemd / nginx サイト反映も同様に共有です。

## Ansible の確認

```bash
export ANSIBLE_CONFIG=tools/isucon-bench/ansible/ansible.cfg
ansible-inventory -i tools/isucon-bench/ansible/inventory.yml --graph
ansible all -i tools/isucon-bench/ansible/inventory.yml -m ping
```

`bench.yml` はセッション ID を Makefile が渡すため、直接実行せず `make bench` を使います。

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
2. `tools/isucon-bench/ansible/group_vars/all.yml` 先頭 … `app_name` / パス / Git / ビルドコマンド / nginx ルーティング
3. （必要なら）`templates/nginx.site.conf.j2` … ルーティング変数で足りない大会向け
4. **GitHub 用 SSH 鍵（デプロイ鍵）**を `tools/isucon-bench/ansible/files/github_id_ed25519[.pub]` に配置（gitignore 済み）  
   → サーバーから private リポジトリを pull するための鍵。詳細は `tools/isucon-bench/ansible/files/README.md`

変数の意味は `group_vars/all.yml` の各行コメントを参照。

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
make pull            # 全サーバーへ git sync
make build           # pull + systemd + ビルド + app/nginx 再起動
make bench           # 計測・解析・回収
make bench PUBLISH=true
make finish          # 本気計測前に計測負荷を外す（= make maji）
make collect SESSION=20260719-123000
```

`make bench` の流れ:

1. pull → app ビルド・再起動、nginx / MariaDB 再起動
2. ログ初期化、pprof 武装
3. ブラウザで公式ベンチ開始を待つ → スコア入力
4. alp / pt-query-digest / pprof / netdata 解析
5. `log/<SESSION>/` へ回収、`REPORT.md` 生成

## Make ターゲット一覧

| ターゲット | 用途 |
| --- | --- |
| `bootstrap` | ツール導入 + git + pprof ON + 設定バックアップ回収 |
| `fleet-setup` | 計測ツールだけ導入 |
| `fleet-enable` / `fleet-disable` | netdata + slow query の ON/OFF |
| `mysql-tune` | MariaDB 性能 cnf 反映 |
| `instrument-on` / `off` | pprof 配置/削除 |
| `pprof-view` / `netdata-view` | 手元でプロファイル / Netdata を見る |
| `restart` | 全ホスト OS 再起動（追試用） |
| `finish` / `maji` | 最終計測前に計測系を外す |

## 構成

```text
Makefile
└── tools/isucon-bench/
    ├── ansible/
    │   ├── ansible.cfg
    │   ├── inventory.yml
    │   ├── group_vars/all.yml      # 大会変数は先頭セクション
    │   ├── setup.yml               # ツール導入（tags: app,db,nginx,mysql,…）
    │   ├── git.yml / build.yml
    │   ├── bench.yml / collect.yml
    │   ├── instrument.yml / monitor.yml
    │   ├── mysql.yml / restart.yml
    │   ├── tasks/                  # 共有タスク
    │   └── templates/              # nginx.site / app.service-override / pprof …
    └── scripts/                    # publish, toggle-pprof, serve-pprof, netdata-view
```

重複していた `enable.yml` / `disable.yml` は `monitor.yml` に統合しています。
systemd / nginx サイト反映は `tasks/app-systemd.yml` と `tasks/nginx-site.yml` にまとめ、
`setup.yml` と `build.yml` から共有しています。

## Ansible の確認

```bash
export ANSIBLE_CONFIG=tools/isucon-bench/ansible/ansible.cfg
ansible-inventory -i tools/isucon-bench/ansible/inventory.yml --graph
ansible all -i tools/isucon-bench/ansible/inventory.yml -m ping
```

`bench.yml` はセッション ID を Makefile が渡すため、直接実行せず `make bench` を使います。

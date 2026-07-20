# pisucon-2026

ISUCONサーバーの再構築、ベンチ前後の計測、結果回収をAnsibleで自動化します。
利用方法と運用コマンドはこのREADMEに集約しています。

## 手元PCの前提ツール

手元PCには `ansible-playbook`、`ansible-inventory`、`jq`、`git`、`gh` が必要です。
Ubuntuでは初回に次を実行します。

```bash
sudo apt update
sudo apt install -y ansible jq git gh
```

## クイックスタート

手元PCで実行します。

```bash
$EDITOR tools/isucon-bench/ansible/inventory.yml
$EDITOR tools/isucon-bench/ansible/group_vars/all.yml

make bootstrap
make bench
```

`inventory.yml` と `group_vars/all.yml` はどちらも最初からGit管理されています。

### 設定ファイル

| ファイル | 内容 | Git管理 |
| --- | --- | --- |
| `tools/isucon-bench/ansible/inventory.yml` | 接続先IP、SSHユーザー、サーバーの役割 | する |
| `tools/isucon-bench/ansible/group_vars/all.yml` | ビルド、ログ、pprof、Git、ローカル出力など全共有設定 | する |

接続先とホストの役割は `inventory.yml`、それ以外の設定は `group_vars/all.yml` に集約します。
CLIとAnsibleは同じファイルを読むため、Shell用の設定ファイルはありません。

inventoryでは各ホストを `app`、`db`、`nginx` に割り当てます。`reporter` はGit revisionと
スコアの基準にするホストをちょうど1台指定します。同じホストを複数グループへ所属させられます。
1台構成では1ホストをすべてのグループへ、3台構成では役割ごとに別ホストを指定します。

### ISUCONサーバーへのSSH

主催側が、チームメンバー各自のGitHubアカウントに登録済みの公開鍵を `isucon` ユーザーへ
配置します。`ssh-copy-id` やAnsibleでの `authorized_keys` 管理は不要です。大会前に各自の
GitHub SSH認証を確認し、ポータルに表示されたIPへ接続します。

```bash
ssh -T git@github.com
ssh isucon@<ポータルに表示されたIP>
```

## 普段のベンチ運用

```bash
make bench               # 計測、解析、手元PCへの回収
make bench PUBLISH=true  # 上記に加えてGitHub Issueを作成
make collect             # 最新結果の回収だけをやり直す
make collect SESSION=20260719-123000
make publish DIR=20260719-123000
```

`make bench` は次の順に処理します。

1. appホストでビルド・再起動、nginxホストで再起動
2. nginxとMySQLの対象ログを初期化
3. pprof取得を開始
4. ターミナルで待機
5. ユーザーがブラウザから公式ベンチを開始
6. 終了後、ユーザーが表示スコアを入力（省略可能）
7. alp、pt-query-digest、pprof、netdataを解析
8. `collect.yml` で手元PCへ回収し、`REPORT.md` を生成

最初の案内ではブラウザの開始画面を準備してEnterを押します。`Profiling is armed` が
表示されたら開始ボタンを押してください。既定では約1分のベンチを覆うためpprofを75秒取得します。

解析結果は `local_log_root/<SESSION>/<HOST>/`、統合レポートは
`local_log_root/<SESSION>/REPORT.md` に保存されます。保持するのは次の解析結果だけです。

- `alp.txt`
- `pt-query-digest.txt`
- `pprof.txt` と再解析・可視化用の `cpu.pprof`
- `netdata.txt`
- スコア、Git revision、開始・終了時刻

nginx access log、MySQL slow log、netdataの生JSONは解析後に削除します。

`make bench` の回収完了後、取得したセッションの最新 `cpu.pprof` は自動で
`http://localhost:6070` のビューへ反映されます。過去の計測結果を開き直すときは、次を実行します。

```bash
make pprof-view
make pprof-view SESSION=20260719-123000
```

コマンド実行後、ブラウザで `http://localhost:6070` を開くと、Graph と Flame Graphを確認できます。
GitHub Issueは手元PCの `gh` から作成するため、競技サーバーにGitHubトークンは置きません。
初回だけ手元PCで `gh auth login` を実行してください。

## pprof

`make bootstrap` はテンプレートから手元の `webapp/go/pprof.go` を生成して、そのファイルだけを
自動commitします。同じコードをサーバーへ配置してビルド・再起動し、localhostの `:6060` で
pprofを待ち受けます。

```bash
make instrument-on   # pprofを配置して有効化
make instrument-off  # pprofを削除して無効化
```

ON/OFF時の `pprof.go` の追加・削除とcommitはコマンド内で完結し、ほかの変更はcommitしません。
手動の `git add` や `git commit` は不要です。

netdataとpprofには多少の負荷があるため、競技終了前または最終スコア計測前は外します。

```bash
make finish
```

`make finish` はnetdataとslow query logを停止し、手元とappホストの `pprof.go` を削除して
削除を自動commitし、アプリを再ビルド・再起動します。

### MariaDBの性能設定

`group_vars/all.yml` の `mysql_innodb_flush_log_at_trx_commit` と
`mysql_sync_binlog` を正とし、`make mysql-tune` でDBホストの
`/etc/mysql/mariadb.conf.d/99-isucon-performance.cnf` へ反映します。既定値は
COMMIT待ちを減らすISUCON向けの `2` と `0` です。OSまたは電源の異常終了時には、直近およそ
1秒の更新を失う可能性があります。

### Goアプリ・Nginxのファイルディスクリプタ上限

`group_vars/all.yml` の `app_limit_nofile` と `nginx_limit_nofile` を正とし、`make fleet-setup` で
各systemd serviceのdrop-inへ反映します。既定値はともに `65535` です。Nginxの
`worker_connections` も `nginx_worker_connections`（既定 `65535`）で設定します。

### netdataを見る

netdataはサーバーのlocalhostだけで待ち受けます。手元PCで次を実行したまま、ブラウザで
`http://localhost:19999` を開きます。

```bash
make netdata-view
make netdata-view HOST=isucon-2  # 複数台構成でホストを指定
```

## サーバー再作成

GitHub上のremote repositoryを先に作成し、`group_vars/all.yml` の
`git_repository` と `remote_project_root` を設定します。SSH URLを使う場合は、GitHubに
登録済みの鍵がサーバーの `isucon` ユーザーで利用できる必要があります。

```bash
make bootstrap
```

`make bootstrap` は全ホストへalp、netdata、pt-query-digestなどを導入し、repositoryを
cloneまたは指定branchへ更新します。nginxは既存の `webapp/public` を直接配信し、`/assets/` と
SPA画面はGoアプリを経由しません。`/api/` と `/initialize` だけがGoアプリへプロキシされます。
未commitの変更は上書きしません。

## よく使うコマンド

### Make

```bash
make help
make bootstrap      # サーバー再作成後の復元
make pull           # GitHubの変更を全サーバーへ反映
make build          # pull + systemd(GOGC/socket) + ビルド + app/nginx 再起動
make fleet-setup    # Git操作なしで計測ツールだけ導入
make bench
make collect
make finish
```

### SSH接続

```bash
ssh -T git@github.com
ssh isucon@<ポータルに表示されたIP>
```

サーバー用に鍵をコピー・登録する必要はありません。GitHubへ登録する公開鍵と対になる
秘密鍵は、各メンバーの手元PCだけで管理します。SSH鍵をまだ作っていない場合だけ、GitHubの
案内に従って作成・登録してください。

### Ansibleの確認

```bash
ANSIBLE_INVENTORY=tools/isucon-bench/ansible/inventory.yml

ansible-inventory -i "$ANSIBLE_INVENTORY" --graph
ansible all -i "$ANSIBLE_INVENTORY" -m ansible.builtin.ping
ansible all -i "$ANSIBLE_INVENTORY" -a 'hostname'
ansible-playbook -vv -i "$ANSIBLE_INVENTORY" tools/isucon-bench/ansible/setup.yml
ansible-lint tools/isucon-bench/ansible
```

`bench.yml` はMakefileがセッションIDなどを渡すため、直接実行せず `make bench` を使います。

### Ansible Vault

Git管理が必要な秘密情報だけをVaultで暗号化します。SSH秘密鍵はVaultへ入れず、
手元PCの `~/.ssh` で管理するのが基本です。

```bash
mkdir -p tools/isucon-bench/ansible/group_vars/all
ansible-vault create tools/isucon-bench/ansible/group_vars/all/vault.yml
ansible-vault edit tools/isucon-bench/ansible/group_vars/all/vault.yml
```

## トラブル時

ベンチや回収に失敗した場合も、サーバー上のセッション成果物は残ります。

```bash
make finish
make collect SESSION=<セッションID>
```

ホスト自体が到達不能な場合、そのホストの後始末や回収はできません。復旧後に
`make finish` と `make collect` を再実行してください。

## 構成

```text
Makefile
├── tools/isucon-bench/ansible/setup.yml       サーバーへ計測ツールを導入
├── tools/isucon-bench/ansible/git.yml         repositoryをclone・更新
├── tools/isucon-bench/ansible/instrument.yml  pprofを配置・削除
├── tools/isucon-bench/ansible/bench.yml       ベンチ前処理・計測・解析
├── tools/isucon-bench/ansible/collect.yml     成果物の回収・レポート統合
├── tools/isucon-bench/ansible/disable.yml     計測負荷を停止
└── tools/isucon-bench/scripts/publish          GitHub Issue投稿
```

pprofは設定の検証によりlocalhostだけで待ち受けます。netdata側もlocalhost bindに設定し、
競技ネットワークへ `19999/tcp` を公開しないでください。

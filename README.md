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
make instrument-on
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

1. appホストでビルド・再起動
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
- `pprof.txt` と再解析用の `cpu.pprof`
- `netdata.txt`
- スコア、Git revision、開始・終了時刻

nginx access log、MySQL slow log、netdataの生JSONは解析後に削除します。
GitHub Issueは手元PCの `gh` から作成するため、競技サーバーにGitHubトークンは置きません。
初回だけ手元PCで `gh auth login` を実行してください。

## 計測機能のON/OFF

netdataとslow query logは `make bootstrap` 後から常時有効です。pprofも一度
`make instrument-on` するとlocalhostのHTTPエンドポイントが常設されますが、
CPU計測負荷が発生するのは取得中だけです。

```bash
make instrument-on   # Goアプリへpprofを追加して再ビルド・再起動
make instrument-off  # pprofを削除して再ビルド・再起動
make fleet-enable    # netdataとslow query logを手動でON
make fleet-disable   # netdataとslow query logを手動でOFF
```

netdataにも多少の負荷があるため、競技終了前または最終スコア計測前はすべて外します。

```bash
make finish
```

`make finish` はnetdataとslow query logを停止し、pprofの生成コードを削除してアプリを
再ビルド・再起動します。

### netdataを見る

netdataはサーバーのlocalhostだけで待ち受けます。手元PCで次を実行したまま、ブラウザで
`http://localhost:19999` を開きます。

```bash
ssh -L 19999:127.0.0.1:19999 isucon@<サーバーIP>
```

## サーバー再作成

GitHub上のremote repositoryを先に作成し、`group_vars/all.yml` の
`git_repository` と `remote_project_root` を設定します。SSH URLを使う場合は、GitHubに
登録済みの鍵がサーバーの `isucon` ユーザーで利用できる必要があります。

```bash
make bootstrap
```

`make bootstrap` は全ホストへalp、netdata、pt-query-digestなどを導入し、repositoryを
cloneまたは指定branchへ更新します。未commitの変更は上書きしません。

## よく使うコマンド

### Make

```bash
make help
make bootstrap      # サーバー再作成後の復元
make pull           # GitHubの変更を全サーバーへ反映
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
├── tools/isucon-bench/ansible/instrument.yml  pprofを追加・削除
├── tools/isucon-bench/ansible/bench.yml       ベンチ前処理・計測・解析
├── tools/isucon-bench/ansible/collect.yml     成果物の回収・レポート統合
├── tools/isucon-bench/ansible/disable.yml     計測負荷を停止
└── tools/isucon-bench/scripts/publish          GitHub Issue投稿
```

pprofは設定の検証によりlocalhostだけで待ち受けます。netdata側もlocalhost bindに設定し、
競技ネットワークへ `19999/tcp` を公開しないでください。

# ISUCON bench kit

GitHub を正にして、複数台へ deploy・ベンチ前後の計測（alp / slow / pprof）を回す。

```text
編集 → git push → make deploy
計測 → make bench
最終前 → make finish
```

触る設定は2つだけ: `tools/isucon-bench/ansible/group_vars/all.yml` と `inventory.yml`

## 初回

```bash
# app_name / git リポジトリ / alp の URI
$EDITOR tools/isucon-bench/ansible/group_vars/all.yml
# 公開IPと役割（1台なら全グループに同じホスト）
$EDITOR tools/isucon-bench/ansible/inventory.yml
# デプロイ鍵を files/ に置く（チームで1組、gitignore 済み）
make bootstrap
```

手元に `ansible` / `ssh` / `go` が必要。サーバーは大会イメージの nginx・mariadb・go を使う（入れ直さない）。

## 複数人

| 誰が | やること |
| --- | --- |
| 全員 | 手元で編集 → `git push`（サーバー直編集しない） |
| 誰か1人 | `make deploy` / `make bench` / `make finish`（同時に叩かない） |
| 開始時 | inventory と `all.yml` を揃えて push |
| 鍵 | デプロイ鍵は `files/` へ。リポに入れず別チャネルで共有 |

`make deploy` はサーバーの未 push 変更を force で消す。

## etc

1. `managed_etc_paths` にパスを書く
2. `make init-git` で `/etc` → `server-config/`
3. 以後は `server-config/` を編集して push → `make deploy`

## コマンド

| コマンド | 意味 |
| --- | --- |
| `make deploy` | sync + etc反映 + build + restart |
| `make bench` | ブラウザベンチ前後の計測・回収（スコア手入力） |
| `make finish` | 計測OFF（最終スコア前） |
| `make fleet-enable` | 計測ON（pprof / slow。git には触らない） |
| `make restart` | OS再起動（追試） |

任意: `make bench PUBLISH=true`（`gh` 認証 + `create_github_issue: true`）

## 前提

- サーバー → GitHub（deploy key）と alp 取得用の外向き HTTPS
- `isucon` の passwordless sudo
- 公式ベンチはブラウザ実行（本ツールは前後処理だけ）

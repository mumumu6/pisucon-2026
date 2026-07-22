# pisucon-2026

覚えることはこれだけ。

```text
編集 → git push → make deploy
計測 → make bench
最終前 → make finish
```

設定は `tools/isucon-bench/ansible/group_vars/all.yml` と `inventory.yml` だけ。

## 初回

```bash
# app_name / IP / git を書く
$EDITOR tools/isucon-bench/ansible/group_vars/all.yml
$EDITOR tools/isucon-bench/ansible/inventory.yml
# デプロイ鍵を files/ に置く
make bootstrap
```

`bootstrap` = init-git（種まき）+ ツール導入 + deploy + 計測ON

## etc

1. `all.yml` の `managed_etc_paths` にパスを書く
2. `make init-git` で `/etc` → `server-config/` にコピーされる
3. 以後は `server-config/` を編集して push → `make deploy`

deploy は `server-config` が無ければエラー（勝手に別場所から取らない）。

## コマンド

| コマンド | 意味 |
| --- | --- |
| `make deploy` | 全台 sync + etc反映 + build + restart |
| `make finish` | 計測OFF |
| `make fleet-enable` | 計測ON |
| `make bench` | ベンチ前後の計測・回収 |
| `make restart` | OS再起動（追試） |

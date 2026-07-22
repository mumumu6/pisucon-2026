# isucon-bench

```text
make bootstrap   # 初回だけ
make deploy      # 普段
make bench       # 計測
make finish      # 最終前
```

設定: `ansible/group_vars/all.yml` + `ansible/inventory.yml`

etc: `managed_etc_paths` → `make init-git` で種まき → `server-config/` を編集 → `make deploy`

pprof は `fleet-enable` / `finish` がサーバー上だけで出し入れする（git には入れない）。

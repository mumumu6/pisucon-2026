# isucon-bench

```text
make bootstrap   # 初回だけ
make deploy      # 普段
make finish      # 最終前
make bench       # 計測
```

設定: `ansible/group_vars/all.yml` + `ansible/inventory.yml`

etc: `managed_etc_paths` → init-git で種まき → `server-config/` を編集 → deploy で `/etc` へ

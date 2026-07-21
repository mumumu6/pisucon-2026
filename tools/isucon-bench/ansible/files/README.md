# GitHub 用 SSH 鍵（デプロイ鍵）

サーバーから private リポジトリへ `git clone` / `git fetch` するための **GitHub SSH 鍵**です。
手元 PC → 競技サーバーへのログイン鍵ではなく、**サーバー → GitHub** 方向の鍵です。

置くファイル（gitignore 済み）:

- `github_id_ed25519` … 秘密鍵
- `github_id_ed25519.pub` … 公開鍵

用意の仕方（どれか）:

1. 競技サーバーに既にある鍵を手元へコピーする  
   `scp isucon@<host>:~/.ssh/id_ed25519{,.pub} tools/isucon-bench/ansible/files/`
2. リポジトリの Settings → Deploy keys に登録した鍵を使う
3. チーム用に作った GitHub SSH 鍵を使う（公開鍵を GitHub に登録済みであること）

`make bootstrap` / `make fleet-setup` が全ホストの `~/.ssh/` へ配ります。

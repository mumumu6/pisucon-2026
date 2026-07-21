# GitHub 用 SSH 鍵（デプロイ鍵）

サーバーから private リポジトリへ `git clone` / `git fetch` するための **GitHub SSH 鍵**です。
手元 PC → 競技サーバーへのログイン鍵ではなく、**サーバー → GitHub** 方向の鍵です。

## チームで1組あればよい

メンバー各自の鍵は不要です。**誰か1人の鍵（またはチーム共用の1組）**を
`files/` に置き、`make bootstrap` で全サーバーへ配れば全員同じリポジトリを pull できます。

置くファイル（gitignore 済み）:

- `github_id_ed25519` … 秘密鍵
- `github_id_ed25519.pub` … 公開鍵

公開鍵は GitHub に1回登録しておけば十分です（リポジトリの Deploy keys、または鍵の持ち主のアカウント）。

用意の仕方（どれか）:

1. 競技サーバーに既にある鍵を手元へコピーする  
   `scp isucon@<host>:~/.ssh/id_ed25519{,.pub} tools/isucon-bench/ansible/files/`
2. リポジトリの Settings → Deploy keys に登録した鍵を使う
3. メンバーのうち1人の GitHub SSH 鍵を使う（公開鍵がその人の GitHub に登録済みであること）

`make bootstrap` / `make fleet-setup` が全ホストの `~/.ssh/` へ配ります。

補足: 競技サーバーへ SSH ログインする鍵は別物で、そちらは主催が各メンバーの GitHub 公開鍵を配る想定です。

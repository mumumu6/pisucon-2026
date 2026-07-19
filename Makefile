SHELL := /usr/bin/env bash

INVENTORY := tools/isucon-bench/ansible/inventory.yml
PLAYBOOK := ansible-playbook --inventory $(INVENTORY)
PUBLISH_SCRIPT := tools/isucon-bench/scripts/publish
BENCH_SESSION ?= $(shell date +%Y%m%d-%H%M%S)

.PHONY: bootstrap pull fleet-setup fleet-enable fleet-disable mysql-tune collect instrument-on instrument-off finish publish bench help

help: ## Makeターゲットと用途を表示する
	@awk 'BEGIN { FS = ":.*## "; printf "Usage: make <target> [OPTION=value]\n\n" } /^[a-zA-Z0-9_-]+:.*## / { printf "  %-18s %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

bootstrap: ## 全サーバーへ計測ツールを導入し、Git repositoryを復元する
	@$(PLAYBOOK) tools/isucon-bench/ansible/setup.yml
	@$(PLAYBOOK) tools/isucon-bench/ansible/git.yml

pull: ## GitHubの指定ブランチを全サーバーへ取得する（ローカルのpushは別途行う）
	@$(PLAYBOOK) tools/isucon-bench/ansible/git.yml

fleet-setup: ## 全サーバーへalp・netdata等の計測ツールを導入する
	@$(PLAYBOOK) tools/isucon-bench/ansible/setup.yml

fleet-enable: ## 全サーバーのnetdataとDBのslow query logを有効にする
	@$(PLAYBOOK) tools/isucon-bench/ansible/enable.yml

fleet-disable: ## netdataとslow query logを止める
	@$(PLAYBOOK) tools/isucon-bench/ansible/disable.yml

mysql-tune: ## Git管理されたMariaDB性能設定をDBホストへ反映し、MariaDBを再起動する
	@$(PLAYBOOK) tools/isucon-bench/ansible/mysql.yml

instrument-on: ## appホストへGo pprofの生成コードを追加し、ビルド・再起動する
	@$(PLAYBOOK) --extra-vars instrument_state=on tools/isucon-bench/ansible/instrument.yml

instrument-off: ## appホストからpprofの生成コードを削除し、ビルド・再起動する
	@$(PLAYBOOK) --extra-vars instrument_state=off tools/isucon-bench/ansible/instrument.yml

bench: ## 計測・解析・回収を行う。Issue投稿: make bench PUBLISH=true
	@status=0; \
	$(PLAYBOOK) --extra-vars "session_id=$(BENCH_SESSION) requested_session=$(BENCH_SESSION)" tools/isucon-bench/ansible/bench.yml || status=$$?; \
	$(PLAYBOOK) --extra-vars "requested_session=$(BENCH_SESSION)" tools/isucon-bench/ansible/collect.yml || exit $$?; \
	if [ $$status -ne 0 ]; then exit $$status; fi; \
	if [ "$(PUBLISH)" = true ]; then $(PUBLISH_SCRIPT) "$(BENCH_SESSION)"; fi

collect: ## 結果だけ再取得する。例: make collect SESSION=20260719-123000
	@$(PLAYBOOK) $(if $(SESSION),--extra-vars "requested_session=$(SESSION)") tools/isucon-bench/ansible/collect.yml

publish: ## 取得済みの解析結果からGitHub Issueを作る。例: make publish DIR=20260719-123000
	@$(PUBLISH_SCRIPT) "$(DIR)"

finish: ## 競技終了前にnetdata・slow query log・pprofをすべて外す
	@$(PLAYBOOK) tools/isucon-bench/ansible/disable.yml
	@$(PLAYBOOK) --extra-vars instrument_state=off tools/isucon-bench/ansible/instrument.yml

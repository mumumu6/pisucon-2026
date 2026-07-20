SHELL := /usr/bin/env bash

INVENTORY := tools/isucon-bench/ansible/inventory.yml
PLAYBOOK := ansible-playbook --inventory $(INVENTORY)
PUBLISH_SCRIPT := tools/isucon-bench/scripts/publish
PPROF_SCRIPT := tools/isucon-bench/scripts/toggle-pprof
PPROF_VIEW_SCRIPT := tools/isucon-bench/scripts/serve-pprof
NETDATA_SCRIPT := tools/isucon-bench/scripts/netdata-view
BENCH_SESSION ?= $(shell date +%Y%m%d-%H%M%S)

.PHONY: bootstrap pull build fleet-setup fleet-enable fleet-disable mysql-tune collect collect-backups instrument-on instrument-off pprof-view netdata-view finish publish bench help

help: ## Makeターゲットと用途を表示する
	@awk 'BEGIN { FS = ":.*## "; printf "Usage: make <target> [OPTION=value]\n\n" } /^[a-zA-Z0-9_-]+:.*## / { printf "  %-18s %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

bootstrap: ## 全サーバーへ計測ツールを導入し、Git repositoryを復元する
	@$(PPROF_SCRIPT) on
	@$(PLAYBOOK) tools/isucon-bench/ansible/setup.yml
	@$(PLAYBOOK) tools/isucon-bench/ansible/git.yml
	@$(PLAYBOOK) --extra-vars instrument_state=on tools/isucon-bench/ansible/instrument.yml
	@$(PLAYBOOK) tools/isucon-bench/ansible/collect-backups.yml

pull: ## GitHubの指定ブランチを全サーバーへ取得する（ローカルのpushは別途行う）
	@$(PLAYBOOK) tools/isucon-bench/ansible/git.yml

build: ## pull のあと systemd設定・ビルド・app/nginx 再起動まで行う（ローカルのpushは別途）
	@$(MAKE) --no-print-directory pull
	@$(PLAYBOOK) tools/isucon-bench/ansible/build.yml
	@$(PLAYBOOK) tools/isucon-bench/ansible/collect-backups.yml

fleet-setup: ## 全サーバーへalp・netdata等の計測ツールを導入する
	@$(PLAYBOOK) tools/isucon-bench/ansible/setup.yml
	@$(PLAYBOOK) tools/isucon-bench/ansible/collect-backups.yml

fleet-enable: ## 全サーバーのnetdataとDBのslow query logを有効にする
	@$(PLAYBOOK) tools/isucon-bench/ansible/enable.yml

fleet-disable: ## netdataとslow query logを止める
	@$(PLAYBOOK) tools/isucon-bench/ansible/disable.yml

mysql-tune: ## Git管理されたMariaDB性能設定をDBホストへ反映し、MariaDBを再起動する
	@$(PLAYBOOK) tools/isucon-bench/ansible/mysql.yml
	@$(PLAYBOOK) tools/isucon-bench/ansible/collect-backups.yml

collect-backups: ## Ansibleのbackup:trueで残った設定バックアップを手元へ回収する
	@$(PLAYBOOK) tools/isucon-bench/ansible/collect-backups.yml

instrument-on: ## appホストへpprofを配置し、ビルド・再起動する
	@$(PPROF_SCRIPT) on
	@$(MAKE) --no-print-directory pull
	@$(PLAYBOOK) --extra-vars instrument_state=on tools/isucon-bench/ansible/instrument.yml

instrument-off: ## appホストからpprofを削除し、ビルド・再起動する
	@$(PPROF_SCRIPT) off
	@$(MAKE) --no-print-directory pull
	@$(PLAYBOOK) --extra-vars instrument_state=off tools/isucon-bench/ansible/instrument.yml

pprof-view: ## 最新のCPUプロファイルをlocalhost:6070へ反映する。例: make pprof-view SESSION=20260719-123000
	@$(PPROF_VIEW_SCRIPT) "$(SESSION)"

netdata-view: ## NetdataへのSSHトンネル。例: make netdata-view HOST=isucon-2 / HOST=all で同時
	@$(NETDATA_SCRIPT) "$(HOST)"

bench: ## 計測・解析・回収を行う。Issue投稿: make bench PUBLISH=true
	@$(MAKE) --no-print-directory pull
	@status=0; \
	$(PLAYBOOK) --extra-vars "session_id=$(BENCH_SESSION) requested_session=$(BENCH_SESSION)" tools/isucon-bench/ansible/bench.yml || status=$$?; \
	$(PLAYBOOK) --extra-vars "requested_session=$(BENCH_SESSION)" tools/isucon-bench/ansible/collect.yml || exit $$?; \
	if [ $$status -ne 0 ]; then exit $$status; fi; \
	$(MAKE) --no-print-directory pprof-view SESSION=$(BENCH_SESSION); \
	if [ "$(PUBLISH)" = true ]; then $(PUBLISH_SCRIPT) "$(BENCH_SESSION)"; fi

collect: ## 結果だけ再取得する。例: make collect SESSION=20260719-123000
	@$(PLAYBOOK) $(if $(SESSION),--extra-vars "requested_session=$(SESSION)") tools/isucon-bench/ansible/collect.yml

publish: ## 取得済みの解析結果からGitHub Issueを作る。例: make publish DIR=20260719-123000
	@$(PUBLISH_SCRIPT) "$(DIR)"

finish: ## 最終計測前にnetdata・slow query log・pprofを外す
	@$(PPROF_SCRIPT) off
	@$(MAKE) --no-print-directory pull
	@$(PLAYBOOK) tools/isucon-bench/ansible/disable.yml
	@$(PLAYBOOK) --extra-vars instrument_state=off tools/isucon-bench/ansible/instrument.yml

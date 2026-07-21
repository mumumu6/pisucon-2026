SHELL := /usr/bin/env bash

ANSIBLE_DIR := tools/isucon-bench/ansible
# ansible.cfg（inventory / pipelining）をこのディレクトリから読む
export ANSIBLE_CONFIG := $(CURDIR)/$(ANSIBLE_DIR)/ansible.cfg
PLAYBOOK := ansible-playbook -i $(ANSIBLE_DIR)/inventory.yml
PUBLISH_SCRIPT := tools/isucon-bench/scripts/publish
PPROF_SCRIPT := tools/isucon-bench/scripts/toggle-pprof
PPROF_VIEW_SCRIPT := tools/isucon-bench/scripts/serve-pprof
GRAFANA_SCRIPT := tools/isucon-bench/scripts/grafana-view
BENCH_SESSION ?= $(shell date +%Y%m%d-%H%M%S)

.PHONY: help bootstrap server-sync pull build restart fleet-setup fleet-enable fleet-disable \
	mysql-tune collect collect-backups pprof-view grafana-view finish publish bench

help: ## Makeターゲットと用途を表示する
	@awk 'BEGIN { FS = ":.*## "; printf "Usage: make <target> [OPTION=value]\n\n" } /^[a-zA-Z0-9_-]+:.*## / { printf "  %-18s %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

bootstrap: ## 全サーバーへ計測ツールを導入し、Git repositoryを復元する
	@$(PPROF_SCRIPT) on
	@$(PLAYBOOK) $(ANSIBLE_DIR)/setup.yml
	@$(PLAYBOOK) $(ANSIBLE_DIR)/git.yml
	@$(PLAYBOOK) --extra-vars monitor_state=on $(ANSIBLE_DIR)/monitor.yml
	@$(MAKE) --no-print-directory collect-backups

server-sync: ## GitHubの指定ブランチを全サーバーへ同期する（ローカルのpushは別途）
	@$(PLAYBOOK) $(ANSIBLE_DIR)/git.yml

pull: ## サーバーから最新ベンチ結果と設定バックアップを手元へ取得。例: make pull / SESSION=20260719-123000
	@$(MAKE) --no-print-directory collect SESSION=$(SESSION)
	@$(MAKE) --no-print-directory collect-backups

build: ## server-sync のあと systemd設定・ビルド・app/nginx 再起動まで行う
	@$(MAKE) --no-print-directory server-sync
	@$(PLAYBOOK) $(ANSIBLE_DIR)/build.yml
	@$(MAKE) --no-print-directory collect-backups

restart: ## 全サーバーを OS 再起動する（追試・永続化確認用）
	@$(PLAYBOOK) $(ANSIBLE_DIR)/restart.yml

fleet-setup: ## 計測ツール導入＋計測系（observe/slow query/pprof）を有効にする
	@$(PPROF_SCRIPT) on
	@$(PLAYBOOK) $(ANSIBLE_DIR)/setup.yml
	@$(MAKE) --no-print-directory server-sync
	@$(PLAYBOOK) --extra-vars monitor_state=on $(ANSIBLE_DIR)/monitor.yml
	@$(MAKE) --no-print-directory collect-backups

fleet-enable: ## 計測系（observe / slow query / pprof）を有効にする
	@$(PPROF_SCRIPT) on
	@$(MAKE) --no-print-directory server-sync
	@$(PLAYBOOK) --extra-vars monitor_state=on $(ANSIBLE_DIR)/monitor.yml
	@$(MAKE) --no-print-directory collect-backups

fleet-disable: ## 計測系（observe / slow query / pprof）を止める
	@$(PPROF_SCRIPT) off
	@$(MAKE) --no-print-directory server-sync
	@$(PLAYBOOK) --extra-vars monitor_state=off $(ANSIBLE_DIR)/monitor.yml
	@$(MAKE) --no-print-directory collect-backups

mysql-tune: ## MariaDB性能設定をDBホストへ反映し、MariaDBを再起動する
	@$(PLAYBOOK) $(ANSIBLE_DIR)/mysql.yml
	@$(MAKE) --no-print-directory collect-backups

collect-backups: ## Ansibleのbackup:trueで残った設定バックアップを手元へ回収する
	@$(PLAYBOOK) $(ANSIBLE_DIR)/collect-backups.yml

pprof-view: ## 最新のCPUプロファイルをlocalhostへ反映。例: make pprof-view SESSION=20260719-123000
	@$(PPROF_VIEW_SCRIPT) "$(SESSION)"

grafana-view: ## GrafanaへのSSHトンネル（reporter）。例: make grafana-view
	@$(GRAFANA_SCRIPT) "$(HOST)"

bench: ## 計測・解析・回収。Issue投稿: make bench PUBLISH=true
	@$(MAKE) --no-print-directory server-sync
	@status=0; \
	$(PLAYBOOK) --extra-vars "session_id=$(BENCH_SESSION) requested_session=$(BENCH_SESSION)" $(ANSIBLE_DIR)/bench.yml || status=$$?; \
	$(PLAYBOOK) --extra-vars "requested_session=$(BENCH_SESSION)" $(ANSIBLE_DIR)/collect.yml || exit $$?; \
	if [ $$status -ne 0 ]; then exit $$status; fi; \
	$(MAKE) --no-print-directory pprof-view SESSION=$(BENCH_SESSION); \
	if [ "$(PUBLISH)" = true ]; then $(PUBLISH_SCRIPT) "$(BENCH_SESSION)"; fi

collect: ## ベンチ結果だけ再取得。例: make collect / SESSION=20260719-123000
	@$(PLAYBOOK) $(if $(SESSION),--extra-vars "requested_session=$(SESSION)") $(ANSIBLE_DIR)/collect.yml

publish: ## 取得済み解析結果からGitHub Issueを作る。例: make publish DIR=20260719-123000
	@$(PUBLISH_SCRIPT) "$(DIR)"

finish: ## 最終計測前に計測系（observe / slow query / pprof）を外す
	@$(MAKE) --no-print-directory fleet-disable

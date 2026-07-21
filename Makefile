SHELL := /usr/bin/env bash

ANSIBLE_DIR := tools/isucon-bench/ansible
export ANSIBLE_CONFIG := $(CURDIR)/$(ANSIBLE_DIR)/ansible.cfg
PLAYBOOK := ansible-playbook -i $(ANSIBLE_DIR)/inventory.yml
PUBLISH_SCRIPT := tools/isucon-bench/scripts/publish
PPROF_SCRIPT := tools/isucon-bench/scripts/toggle-pprof
PPROF_VIEW_SCRIPT := tools/isucon-bench/scripts/serve-pprof
NETDATA_VIEW_SCRIPT := tools/isucon-bench/scripts/netdata-view
BENCH_SESSION ?= $(shell date +%Y%m%d-%H%M%S)

.PHONY: help bootstrap server-sync pull deploy restart \
	mysql-tune collect collect-backups pprof-view netdata-view \
	fleet-enable fleet-disable finish publish bench get-log-detail

help: ## Makeターゲットと用途を表示する
	@awk 'BEGIN { FS = ":.*## "; printf "Usage: make <target> [OPTION=value]\n\n" } /^[a-zA-Z0-9_-]+:.*## / { printf "  %-18s %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

bootstrap: ## 計測ツール導入 + git sync + 計測系 ON
	@$(PLAYBOOK) $(ANSIBLE_DIR)/setup.yml
	@$(MAKE) --no-print-directory fleet-enable

server-sync: ## GitHubの指定ブランチを全サーバーへ同期する
	@$(PLAYBOOK) $(ANSIBLE_DIR)/git.yml

pull: ## ベンチ結果と設定バックアップを手元へ取得。例: make pull SESSION=20260719-123000
	@$(MAKE) --no-print-directory collect SESSION=$(SESSION)
	@$(MAKE) --no-print-directory collect-backups

deploy: ## server-sync + systemd/ビルド/nginx 反映
	@$(MAKE) --no-print-directory server-sync
	@$(PLAYBOOK) $(ANSIBLE_DIR)/deploy.yml

restart: ## 全サーバーを OS 再起動する（追試用）
	@$(PLAYBOOK) $(ANSIBLE_DIR)/reboot.yml

fleet-enable: ## 計測系 ON（pprof.go → sync → netdata/slow query/ビルド）
	@$(PPROF_SCRIPT) on
	@$(MAKE) --no-print-directory server-sync
	@$(PLAYBOOK) --extra-vars monitor_state=on $(ANSIBLE_DIR)/monitor.yml

fleet-disable: ## 計測系 OFF
	@$(PPROF_SCRIPT) off
	@$(MAKE) --no-print-directory server-sync
	@$(PLAYBOOK) --extra-vars monitor_state=off $(ANSIBLE_DIR)/monitor.yml

finish: fleet-disable ## 最終計測前に計測系を外す（= fleet-disable）

mysql-tune: ## MariaDB性能設定を反映する
	@$(PLAYBOOK) $(ANSIBLE_DIR)/mysql.yml

collect-backups: ## Ansible backup ファイルを手元へ回収する
	@$(PLAYBOOK) $(ANSIBLE_DIR)/collect-backups.yml

pprof-view: ## CPUプロファイルをlocalhostで開く。例: make pprof-view SESSION=...
	@$(PPROF_VIEW_SCRIPT) "$(SESSION)"

netdata-view: ## NetdataへSSHトンネル。例: make netdata-view HOST=all
	@$(NETDATA_VIEW_SCRIPT) "$(HOST)"

bench: ## 計測・解析・回収。Issue投稿: make bench PUBLISH=true
	@$(MAKE) --no-print-directory server-sync
	@status=0; \
	$(PLAYBOOK) --extra-vars "session_id=$(BENCH_SESSION) requested_session=$(BENCH_SESSION)" $(ANSIBLE_DIR)/bench.yml || status=$$?; \
	$(PLAYBOOK) --extra-vars "requested_session=$(BENCH_SESSION)" $(ANSIBLE_DIR)/collect.yml || exit $$?; \
	if [ $$status -ne 0 ]; then exit $$status; fi; \
	$(MAKE) --no-print-directory pprof-view SESSION=$(BENCH_SESSION); \
	if [ "$(PUBLISH)" = true ]; then $(PUBLISH_SCRIPT) "$(BENCH_SESSION)"; fi

collect: ## ベンチ結果だけ再取得。例: make collect SESSION=...
	@$(PLAYBOOK) $(if $(SESSION),--extra-vars "requested_session=$(SESSION)") $(ANSIBLE_DIR)/collect.yml

get-log-detail: ## 詳細ログを手元へ。次のbench前に。例: make get-log-detail / SESSION=20260719-123000
	@$(PLAYBOOK) --extra-vars "requested_session=$(SESSION) raw_log_id=$(or $(SESSION),$(shell date +%Y%m%d-%H%M%S))" $(ANSIBLE_DIR)/fetch-logs.yml

publish: ## 取得済み結果からGitHub Issueを作る。例: make publish DIR=...
	@$(PUBLISH_SCRIPT) "$(DIR)"

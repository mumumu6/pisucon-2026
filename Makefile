SHELL := /usr/bin/env bash

ANSIBLE_DIR := tools/isucon-bench/ansible
export ANSIBLE_CONFIG := $(CURDIR)/$(ANSIBLE_DIR)/ansible.cfg
PLAYBOOK := ansible-playbook -i $(ANSIBLE_DIR)/inventory.yml
PUBLISH_SCRIPT := tools/isucon-bench/scripts/publish
PPROF_SCRIPT := tools/isucon-bench/scripts/toggle-pprof
PPROF_VIEW_SCRIPT := tools/isucon-bench/scripts/serve-pprof
NETDATA_VIEW_SCRIPT := tools/isucon-bench/scripts/netdata-view
BENCH_SESSION ?= $(shell date +%Y%m%d-%H%M%S)

.PHONY: help bootstrap init-git server-sync deploy restart \
	collect pprof-view netdata-view \
	fleet-enable fleet-disable finish publish bench get-log-detail

help: ## Makeターゲットと用途を表示する
	@awk 'BEGIN { FS = ":.*## "; printf "Usage: make <target> [OPTION=value]\n\n" } /^[a-zA-Z0-9_-]+:.*## / { printf "  %-18s %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

bootstrap: ## 初回: git化 + ツール導入 + deploy + 計測ON
	@$(MAKE) --no-print-directory init-git
	@$(PLAYBOOK) $(ANSIBLE_DIR)/setup.yml
	@$(MAKE) --no-print-directory deploy
	@$(MAKE) --no-print-directory fleet-enable

init-git: ## サーバーで git 化、/etc → server-config 種まき
	@$(PLAYBOOK) $(ANSIBLE_DIR)/init-git.yml

server-sync: ## GitHub → 全サーバー
	@$(PLAYBOOK) $(ANSIBLE_DIR)/git.yml

deploy: ## sync + server-config→/etc + build + restart
	@$(MAKE) --no-print-directory server-sync
	@$(PLAYBOOK) $(ANSIBLE_DIR)/deploy.yml

restart: ## 全サーバー OS 再起動（追試用）
	@$(PLAYBOOK) $(ANSIBLE_DIR)/reboot.yml

fleet-enable: ## 計測 ON
	@$(PPROF_SCRIPT) on
	@$(MAKE) --no-print-directory server-sync
	@$(PLAYBOOK) --extra-vars monitor_state=on $(ANSIBLE_DIR)/monitor.yml

fleet-disable: ## 計測 OFF
	@$(PPROF_SCRIPT) off
	@$(MAKE) --no-print-directory server-sync
	@$(PLAYBOOK) --extra-vars monitor_state=off $(ANSIBLE_DIR)/monitor.yml

finish: fleet-disable ## 最終前に計測 OFF

pprof-view: ## CPUプロファイルを開く。例: make pprof-view SESSION=...
	@$(PPROF_VIEW_SCRIPT) "$(SESSION)"

netdata-view: ## Netdata を SSH トンネルで開く
	@$(NETDATA_VIEW_SCRIPT)

bench: ## 計測・解析・回収。Issue: make bench PUBLISH=true
	@$(MAKE) --no-print-directory server-sync
	@status=0; \
	$(PLAYBOOK) --extra-vars "session_id=$(BENCH_SESSION) requested_session=$(BENCH_SESSION)" $(ANSIBLE_DIR)/bench.yml || status=$$?; \
	$(PLAYBOOK) --extra-vars "requested_session=$(BENCH_SESSION)" $(ANSIBLE_DIR)/collect.yml || exit $$?; \
	if [ $$status -ne 0 ]; then exit $$status; fi; \
	$(MAKE) --no-print-directory pprof-view SESSION=$(BENCH_SESSION); \
	if [ "$(PUBLISH)" = true ]; then $(PUBLISH_SCRIPT) "$(BENCH_SESSION)"; fi

collect: ## 結果だけ再取得。例: make collect SESSION=...
	@$(PLAYBOOK) $(if $(SESSION),--extra-vars "requested_session=$(SESSION)") $(ANSIBLE_DIR)/collect.yml

get-log-detail: ## 詳細ログを手元へ。例: make get-log-detail SESSION=...
	@$(PLAYBOOK) --extra-vars "requested_session=$(SESSION) raw_log_id=$(or $(SESSION),$(shell date +%Y%m%d-%H%M%S))" $(ANSIBLE_DIR)/fetch-logs.yml

publish: ## Issue 作成。例: make publish DIR=...
	@$(PUBLISH_SCRIPT) "$(DIR)"

SHELL := /usr/bin/env bash

TOOLS := ./tools/isucon-bench/bin/isucon-bench

.PHONY: setup bootstrap fleet-setup fleet-enable fleet-disable collect instrument-on instrument-off finish publish bench help

help: ## Makeターゲットと用途を表示する
	@awk 'BEGIN { FS = ":.*## "; printf "Usage: make <target> [OPTION=value]\n\n" } /^[a-zA-Z0-9_-]+:.*## / { printf "  %-18s %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

setup: ## 手元PCへAnsible・jq・git・ghを導入する（初回のみ）
	@$(TOOLS) setup

bootstrap: ## サーバー再作成後に計測ツールを導入し、既存remoteからGit cloneする
	@$(TOOLS) fleet bootstrap

fleet-setup: ## inventory内の全サーバーへalp・netdata等の計測ツールを導入する
	@$(TOOLS) fleet setup

fleet-enable: ## 全サーバーのnetdataとDBのslow query logを有効にする
	@$(TOOLS) fleet enable

fleet-disable: ## netdataとslow query logを止め、競技用サーバーの計測負荷を外す
	@$(TOOLS) fleet disable

collect: ## 結果だけ再取得する（通常は不要）。指定例: make collect SESSION=20260719-123000
	@$(TOOLS) fleet collect "$(SESSION)"

instrument-on: ## appホストへGo pprofの生成コードを追加し、ビルド・再起動する
	@$(TOOLS) fleet instrument on

instrument-off: ## appホストからpprofの生成コードを削除し、ビルド・再起動する
	@$(TOOLS) fleet instrument off

finish: ## 競技終了前にnetdata・slow query log・pprofをすべて外す
	@$(TOOLS) fleet disable
	@$(TOOLS) fleet instrument off

publish: ## 取得済みの解析結果からGitHub Issueを作る。指定例: make publish DIR=20260719-123000
	@$(TOOLS) publish "$(DIR)"

bench: ## 計測・解析・回収を行う。解析結果をIssue投稿する場合: make bench PUBLISH=true
	@$(TOOLS) fleet bench $(if $(filter true,$(PUBLISH)),--publish,)

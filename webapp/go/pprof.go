// pprof 有効化用の追加ファイル。app_working_dir に配置してビルドする。
// 待受は localhost:6060 固定（group_vars の pprof_url と揃えること）。
// Jinja 変数は使わない。ポートを変えるならこの ListenAndServe と all.yml の pprof_url を両方直す。
package main

import (
	"net/http"
	_ "net/http/pprof"
)

func init() {
	go http.ListenAndServe("127.0.0.1:6060", nil)
}

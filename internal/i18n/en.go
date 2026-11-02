package i18n

// enTexts 界面文案英文表（D275）：与 zh.go 同 key、同顺序、同占位符名——并排看能一眼对齐。
// 顺序与 key 都不许各自演化，改一边必须改另一边（同构测试守着）。
var enTexts = map[string]string{

	// 启动与首跑
	"boot.config_written": "Wrote config: {path}\nprofile: {profile} (pointer: {pointer})\n",
	"boot.first_run.hint": "Edit model.providers (base_url / models / api_key), set the key environment variable, then run again.\n" +
		"  api_key is best written as secret:<ENV_NAME> (for example secret:AQUARIUS_OPENAI_KEY); a literal key works too, but startup warns about it.\n",
}

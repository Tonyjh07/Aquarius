package i18n

// enBootTexts 启动与首跑：与 zh_boot.go 同 key、同顺序、同占位符名。
var enBootTexts = map[string]string{
	"boot.config_written": "Wrote config: {path}\nprofile: {profile} (pointer: {pointer})\n",
	"boot.first_run.hint": "Edit model.providers (base_url / models / api_key), set the key environment variable, then run again.\n" +
		"  api_key is best written as secret:<ENV_NAME> (for example secret:AQUARIUS_OPENAI_KEY); a literal key works too, but startup warns about it.\n",
}

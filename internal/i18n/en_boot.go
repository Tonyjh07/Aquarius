package i18n

// enBootTexts 启动与首跑：与 zh_boot.go 同 key、同顺序、同占位符名。
var enBootTexts = map[string]string{
	"boot.config_written": "Wrote config: {path}\nprofile: {profile} (pointer: {pointer})\n",
	"boot.first_run.hint": "Edit model.providers (base_url / models / api_key), set the key environment variable, then run again.\n" +
		"  api_key is best written as secret:<ENV_NAME> (for example secret:DEEPSEEK_API_KEY); a literal key works too, but startup warns about it.\n",

	// top-level panic reporting (D299)
	"cmd.crash.saved":        "The program crashed; the report was written to: {path}",
	"cmd.crash.write_failed": "The program crashed and the report could not be written: {error}",
	"cmd.crash.notice":       "The last run crashed; report: {path} (give it a look before sending it out - it may contain conversation text)",
}

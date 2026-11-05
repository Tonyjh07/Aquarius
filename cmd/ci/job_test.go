package main

import (
	"strings"
	"testing"
)

func TestSelectJobsByProfile(t *testing.T) {
	all := defaultJobs(".", ".")

	cases := []struct {
		prof profile
		want []string
	}{
		{profFast, []string{"fmt", "docsync", "vet"}},
		{profDefault, []string{"fmt", "docsync", "vet", "build", "test:zh", "test:en",
			"aqvalidate", "property", "envclean"}},
		{profFull, []string{"fmt", "docsync", "vet", "build", "test:zh", "test:en",
			"aqvalidate", "property", "envclean", "race", "linuxgate", "vuln"}},
	}
	for _, c := range cases {
		var got []string
		for _, j := range selectJobs(all, c.prof, "") {
			got = append(got, j.name)
		}
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s 档：得到 %v，要 %v", c.prof, got, c.want)
		}
	}
}

func TestSelectJobsOnlyOverridesProfile(t *testing.T) {
	all := defaultJobs(".", ".")
	got := selectJobs(all, profFast, "race, fmt")
	if len(got) != 2 || got[0].name != "fmt" || got[1].name != "race" {
		t.Fatalf("只跑指定项时得到 %d 项；且顺序应按声明序而不是参数序", len(got))
	}
}

func TestEveryJobHasNoteAndTimeout(t *testing.T) {
	for _, j := range defaultJobs(".", ".") {
		if j.note == "" {
			t.Errorf("%s 缺少一句话说明：失败时用户要知道这条挡什么", j.name)
		}
		if j.timeout <= 0 {
			t.Errorf("%s 没有超时；卡住的 job 会拖死整轮", j.name)
		}
		if len(j.steps) == 0 {
			t.Errorf("%s 没有步骤", j.name)
		}
	}
}

func TestJobNamesUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, j := range defaultJobs(".", ".") {
		if seen[j.name] {
			t.Errorf("重名 job：%s，-only 会指不准", j.name)
		}
		seen[j.name] = true
	}
}

func TestWavesGroupsAdjacent(t *testing.T) {
	all := selectJobs(defaultJobs(".", "."), profDefault, "")
	ws := waves(all)

	var got []string
	for _, w := range ws {
		var names []string
		for _, j := range w {
			names = append(names, j.name)
		}
		got = append(got, strings.Join(names, "+"))
	}
	want := []string{"fmt+docsync+vet+build", "test:zh+test:en", "aqvalidate", "property", "envclean"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("波次切分不对：\n得到 %v\n要  %v", got, want)
	}
}

func TestParallelismDefaults(t *testing.T) {
	if n := parallelism(profFast, 0); n < 2 {
		t.Errorf("fast 档默认应并行，得到 %d", n)
	}
	if n := parallelism(profDefault, 0); n != 1 {
		t.Errorf("default 档默认应串行（全量测试自己就会吃满核），得到 %d", n)
	}
	if n := parallelism(profDefault, 3); n != 3 {
		t.Errorf("-jobs 应覆盖默认值，得到 %d", n)
	}
}

func TestParseProfileRejectsUnknown(t *testing.T) {
	if _, ok := parseProfile("slow"); ok {
		t.Error("未知档位不该被接受")
	}
	if p, ok := parseProfile("full"); !ok || p != profFull {
		t.Error("full 应解析为 profFull")
	}
}

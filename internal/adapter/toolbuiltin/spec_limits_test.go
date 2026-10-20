package toolbuiltin

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestSpecsDeriveLimitsFromConstants 描述里的数字必须与常量同源（D171）。
//
// 手写数字不是「有点旧」，而是**说谎**：常量改了、描述没跟，模型会按错的上限规划分页，
// 然后拿到一段被截断的结果还以为读完了。这里把每个声明里出现的上限逐个对账——
// 改了常量却忘了改描述（或反过来），测试就红。
func TestSpecsDeriveLimitsFromConstants(t *testing.T) {
	specs := map[string]string{}
	for _, tl := range New(Deps{}) {
		s := tl.Spec()
		specs[s.Name] = s.Description + "\n" + string(s.Schema)
	}

	cases := []struct {
		tool  string
		limit string
	}{
		{"file_read", fmt.Sprint(maxReadLines)},
		{"file_read", fmt.Sprint(maxReadPageChars)},
		{"file_list", fmt.Sprint(maxListEntries)},
		{"file_search", fmt.Sprint(maxSearchHits)},
		{"job_logs", fmt.Sprint(defaultJobLogsTail)},
		{"job_start", fmt.Sprint(maxJobTimeoutSec)},
		{"sleep", fmt.Sprint(port.MaxCallTimeoutSec)},
	}
	for _, tc := range cases {
		text, ok := specs[tc.tool]
		if !ok {
			t.Fatalf("%s not registered", tc.tool)
		}
		if !strings.Contains(text, tc.limit) {
			t.Fatalf("%s description/schema lacks its limit %q:\n%s", tc.tool, tc.limit, text)
		}
	}
}

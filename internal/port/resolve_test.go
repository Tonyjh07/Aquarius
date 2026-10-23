package port

import "testing"

func TestMatchPrefix(t *testing.T) {
	ids := []JobID{"job_abcd1234", "job_abef5678", "job_zzzz0000"}
	cases := []struct {
		name    string
		arg     string
		want    JobID
		wantHit int
		wantOK  bool
	}{
		{"exact beats prefix", "job_abcd1234", "job_abcd1234", 0, true},
		{"unique prefix", "job_z", "job_zzzz0000", 0, true},
		{"no match", "job_qq", "", 0, false},
		{"ambiguous", "job_ab", "", 2, false},
		{"exact full id of one hit", "job_abef5678", "job_abef5678", 0, true},
	}
	for _, c := range cases {
		got, hits, ok := MatchPrefix(ids, c.arg)
		if ok != c.wantOK || got != c.want || len(hits) != c.wantHit {
			t.Errorf("%s: MatchPrefix(%q) = %q, %v (hits %d), want %q, %v (hits %d)",
				c.name, c.arg, got, ok, len(hits), c.want, c.wantOK, c.wantHit)
		}
	}
}

// TestMatchPrefixKeepsHitOrder 命中集必须按传入顺序返回：歧义提示的可读性依赖它。
func TestMatchPrefixKeepsHitOrder(t *testing.T) {
	ids := []WatchID{"w_a1", "w_a3", "w_a2"}
	_, hits, ok := MatchPrefix(ids, "w_a")
	if ok {
		t.Fatal("三个前缀命中应为歧义")
	}
	want := []string{"w_a1", "w_a3", "w_a2"}
	for i := range want {
		if hits[i] != want[i] {
			t.Fatalf("命中顺序被改变: got %v, want %v", hits, want)
		}
	}
}

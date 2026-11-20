package port

import (
	"reflect"
	"testing"
)

func TestServerCapsFromNames(t *testing.T) {
	got := ServerCapsFromNames([]string{" Title ", "read_url", "bogus", "FILES"})
	want := ServerCaps{Title: true, ReadURL: true, Files: true}
	if got != want {
		t.Fatalf("ServerCapsFromNames = %+v, want %+v（认不出的名字应忽略）", got, want)
	}
}

func TestServerCapsHasIsCaseInsensitive(t *testing.T) {
	c := ServerCaps{Title: true}
	for _, name := range []string{"title", "TITLE", " title "} {
		if !c.Has(name) {
			t.Fatalf("Has(%q) = false, want true", name)
		}
	}
	if c.Has(ServerCapFiles) {
		t.Fatal("Has(files) = true, want false")
	}
}

func TestServerCapsDrop(t *testing.T) {
	c := ServerCapsFromNames(ServerCapNames())
	c = c.Drop(" Title ").Drop("bogus")
	if c.Title {
		t.Fatal("Drop 之后 title 仍为真")
	}
	if len(c.Names()) != len(ServerCapNames())-1 {
		t.Fatalf("Names() = %v, want 少一项", c.Names())
	}
}

func TestServerCapsNamesOrder(t *testing.T) {
	c := ServerCaps{Safety: true, Title: true, WebSearch: true}
	want := []string{ServerCapTitle, ServerCapWebSearch, ServerCapSafety}
	if got := c.Names(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Names() = %v, want %v（顺序固定，与 ServerCapNames 一致）", got, want)
	}
	if !reflect.DeepEqual(ServerCaps{}.Names(), []string{}) {
		t.Fatal("零值应给出空清单而不是 nil")
	}
}

func TestIsServerCap(t *testing.T) {
	for _, n := range ServerCapNames() {
		if !IsServerCap(n) {
			t.Fatalf("IsServerCap(%q) = false", n)
		}
	}
	if IsServerCap("titles") || IsServerCap("") {
		t.Fatal("词表外的名字应判假")
	}
}

// TestServerCapsAnd 取交：两项都认才算认（降级链用）。
func TestServerCapsAnd(t *testing.T) {
	a := ServerCaps{Title: true, ReadURL: true, Safety: true}
	b := ServerCaps{Title: true, Files: true}
	got := a.And(b)
	if !got.Title || got.ReadURL || got.Safety || got.Files {
		t.Fatalf("And 结果不对：%+v", got)
	}
	if got := (ServerCaps{Title: true}).And(ServerCaps{}); got != (ServerCaps{}) {
		t.Fatalf("与空集取交应为空：%+v", got)
	}
}

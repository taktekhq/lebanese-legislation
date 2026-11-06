package memo

import (
	"testing"
	"time"
)

func TestMap(t *testing.T) {
	var m Map
	k := Key("SELECT count(*) FROM t WHERE a = $1", 7)
	if _, ok := m.Get(k); ok {
		t.Fatal("empty memo answered")
	}
	m.Put(k, 42)
	if v, ok := m.Get(k); !ok || v.(int) != 42 {
		t.Fatalf("got %v %v", v, ok)
	}
	if _, ok := m.Get(Key("SELECT count(*) FROM t WHERE a = $1", 8)); ok {
		t.Fatal("another argument answered")
	}
	short := Map{TTL: time.Millisecond, Max: 2}
	short.Put("a", 1)
	time.Sleep(3 * time.Millisecond)
	if _, ok := short.Get("a"); ok {
		t.Fatal("expired entry answered")
	}
	short.Put("a", 1)
	short.Put("b", 2)
	short.Put("c", 3) // full: starts again
	if _, ok := short.Get("a"); ok {
		t.Fatal("a full memo kept its old keys")
	}
	var nilMap *Map
	nilMap.Put("x", 1)
	if _, ok := nilMap.Get("x"); ok {
		t.Fatal("nil memo answered")
	}
}

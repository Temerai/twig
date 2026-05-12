package graphagent

import (
	"fmt"
	"testing"
)

func TestFileCacheGetMiss(t *testing.T) {
	c := newFileCache(2)
	_, ok := c.get("nonexistent")
	if ok {
		t.Fatal("expected miss on empty cache")
	}
}

func TestFileCachePutAndGet(t *testing.T) {
	c := newFileCache(2)
	lines := []string{"line1", "line2"}
	c.put("file.go", lines)

	got, ok := c.get("file.go")
	if !ok {
		t.Fatal("expected hit after put")
	}
	if len(got) != 2 || got[0] != "line1" || got[1] != "line2" {
		t.Fatalf("unexpected lines: %v", got)
	}
}

func TestFileCacheEvictsOldest(t *testing.T) {
	c := newFileCache(2)
	c.put("a", []string{"a"})
	c.put("b", []string{"b"})
	c.put("c", []string{"c"}) // should evict "a"

	if _, ok := c.get("a"); ok {
		t.Fatal("expected 'a' to be evicted")
	}
	if _, ok := c.get("b"); !ok {
		t.Fatal("expected 'b' to still be cached")
	}
	if _, ok := c.get("c"); !ok {
		t.Fatal("expected 'c' to still be cached")
	}
}

func TestFileCacheLRUOrder(t *testing.T) {
	c := newFileCache(2)
	c.put("a", []string{"a"})
	c.put("b", []string{"b"})

	// Access "a" to make it recently used
	c.get("a")

	// Insert "c" — should evict "b" (least recently used), not "a"
	c.put("c", []string{"c"})

	if _, ok := c.get("b"); ok {
		t.Fatal("expected 'b' to be evicted")
	}
	if _, ok := c.get("a"); !ok {
		t.Fatal("expected 'a' to still be cached after recent access")
	}
	if _, ok := c.get("c"); !ok {
		t.Fatal("expected 'c' to still be cached")
	}
}

func TestFileCachePutUpdatesExisting(t *testing.T) {
	c := newFileCache(2)
	c.put("a", []string{"old"})
	c.put("a", []string{"new"})

	got, ok := c.get("a")
	if !ok {
		t.Fatal("expected hit")
	}
	if got[0] != "new" {
		t.Fatalf("expected updated value, got %v", got)
	}
	if c.ll.Len() != 1 {
		t.Fatalf("expected 1 element in list, got %d", c.ll.Len())
	}
}

func TestFileCacheMaxEntries128(t *testing.T) {
	if fileCacheMaxEntries != 128 {
		t.Fatalf("expected fileCacheMaxEntries=128, got %d", fileCacheMaxEntries)
	}

	// Behavioral test: fill to capacity, verify all present, then overflow by one.
	c := newFileCache(128)
	for i := 0; i < 128; i++ {
		key := fmt.Sprintf("file%d.go", i)
		c.put(key, []string{key})
	}
	// All 128 entries must be present.
	for i := 0; i < 128; i++ {
		key := fmt.Sprintf("file%d.go", i)
		if _, ok := c.get(key); !ok {
			t.Fatalf("expected %q to be present after filling to capacity", key)
		}
	}

	// Insert entry 129 — oldest entry (file0.go) must be evicted.
	c.put("file128.go", []string{"file128.go"})

	if c.ll.Len() != 128 {
		t.Fatalf("expected 128 entries after overflow, got %d", c.ll.Len())
	}
	if _, ok := c.get("file128.go"); !ok {
		t.Fatal("expected new entry 'file128.go' to be present")
	}
	if _, ok := c.get("file0.go"); ok {
		t.Fatal("expected oldest entry 'file0.go' to be evicted")
	}
}

func TestFileCachePutUpdateAtCapacity(t *testing.T) {
	c := newFileCache(2)
	c.put("a", []string{"a"})
	c.put("b", []string{"b"})

	// Update "a" at capacity — should NOT evict anything.
	c.put("a", []string{"a_updated"})

	if c.ll.Len() != 2 {
		t.Fatalf("expected 2 entries after update-in-place, got %d", c.ll.Len())
	}
	got, ok := c.get("a")
	if !ok || got[0] != "a_updated" {
		t.Fatalf("expected 'a' to have updated value, got %v", got)
	}
	if _, ok := c.get("b"); !ok {
		t.Fatal("expected 'b' to still be present after in-place update")
	}
}

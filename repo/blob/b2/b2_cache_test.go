package b2

import (
	"strconv"
	"testing"
	"time"

	"github.com/kurin/blazer/b2"
)

func TestB2CacheRingSize(t *testing.T) {
	cache := newB2Cache(10)

	type blobData struct {
		id string
		o  *b2.Object
	}

	var testBlobs []blobData

	for i := 0; i < 11; i++ {
		testBlobs = append(testBlobs, blobData{
			id: strconv.Itoa(i),
			o:  &b2.Object{},
		})
	}

	// Add the first 10 elements. They all should be present.
	for i := 0; i < 10; i++ {
		cache.Add(testBlobs[i].id, testBlobs[i].o)
	}

	for i := 0; i < 10; i++ {
		o := cache.Get(testBlobs[i].id)
		if o != testBlobs[i].o {
			t.Errorf("unexpected result for entry %d: %v != %v", i, o, testBlobs[i].o)
		}
	}

	// Add the 11th one ... 0 should be out then.
	cache.Add(testBlobs[10].id, testBlobs[10].o)

	if cache.Get(testBlobs[0].id) != nil {
		t.Errorf("element 0 should be removed")
	}

	for i := 1; i < 11; i++ {
		o := cache.Get(testBlobs[i].id)
		if o != testBlobs[i].o {
			t.Errorf("unexpected result for entry %d: %v != %v", i, o, testBlobs[i].o)
		}
	}
}

func TestB2CacheExpiration(t *testing.T) {
	cache := newB2Cache(10)
	cache.Add("foo", &b2.Object{})

	if cache.Get("foo") == nil {
		t.Error("entry before expiration should be found")
	}

	// cheat by modifying private var:
	cache.entries["foo"].ts = time.Now().Add(-2 * time.Minute)

	if cache.Get("foo") != nil {
		t.Error("entry after expiration should not be found")
	}
}

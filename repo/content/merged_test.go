package content

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/pkg/errors"
)

func TestMerged(t *testing.T) {
	i1, err := indexWithItems(
		Info{ID: "aabbcc", TimestampSeconds: 1, PackBlobID: "xx", PackOffset: 11},
		Info{ID: "ddeeff", TimestampSeconds: 1, PackBlobID: "xx", PackOffset: 111},
		Info{ID: "z010203", TimestampSeconds: 1, PackBlobID: "xx", PackOffset: 111},
		Info{ID: "de1e1e", TimestampSeconds: 4, PackBlobID: "xx", PackOffset: 111},
	)
	if err != nil {
		t.Fatalf("can't create index: %v", err)
	}

	i2, err := indexWithItems(
		Info{ID: "aabbcc", TimestampSeconds: 3, PackBlobID: "yy", PackOffset: 33},
		Info{ID: "xaabbcc", TimestampSeconds: 1, PackBlobID: "xx", PackOffset: 111},
		Info{ID: "de1e1e", TimestampSeconds: 4, PackBlobID: "xx", PackOffset: 222, Deleted: true},
	)
	if err != nil {
		t.Fatalf("can't create index: %v", err)
	}

	i3, err := indexWithItems(
		Info{ID: "aabbcc", TimestampSeconds: 2, PackBlobID: "zz", PackOffset: 22},
		Info{ID: "ddeeff", TimestampSeconds: 1, PackBlobID: "zz", PackOffset: 222},
		Info{ID: "k010203", TimestampSeconds: 1, PackBlobID: "xx", PackOffset: 111},
		Info{ID: "k020304", TimestampSeconds: 1, PackBlobID: "xx", PackOffset: 111},
	)
	if err != nil {
		t.Fatalf("can't create index: %v", err)
	}

	m := mergedIndex{i1, i2, i3}

	i, err := m.GetInfo("aabbcc")
	if err != nil || i == nil {
		t.Fatalf("unable to get info: %v", err)
	}

	if got, want := i.PackOffset, uint32(33); got != want {
		t.Errorf("invalid pack offset %v, wanted %v", got, want)
	}

	var inOrder []ID

	assertNoError(t, m.Iterate("", func(i Info) error {
		inOrder = append(inOrder, i.ID)
		if i.ID == "de1e1e" {
			if i.Deleted {
				t.Errorf("iteration preferred deleted content over non-deleted")
			}
		}
		return nil
	}))

	if i, err := m.GetInfo("de1e1e"); err != nil {
		t.Errorf("error getting deleted content info: %v", err)
	} else if i.Deleted {
		t.Errorf("GetInfo preferred deleted content over non-deleted")
	}

	expectedInOrder := []ID{
		"aabbcc",
		"ddeeff",
		"de1e1e",
		"k010203",
		"k020304",
		"xaabbcc",
		"z010203",
	}
	if !reflect.DeepEqual(inOrder, expectedInOrder) {
		t.Errorf("unexpected items in order: %v, wanted %v", inOrder, expectedInOrder)
	}

	if err := m.Close(); err != nil {
		t.Errorf("unexpected error in Close(): %v", err)
	}
}

func indexWithItems(items ...Info) (packIndex, error) {
	b := make(packIndexBuilder)

	for _, it := range items {
		b.Add(it)
	}

	var buf bytes.Buffer
	if err := b.Build(&buf); err != nil {
		return nil, errors.Wrap(err, "build error")
	}

	return openPackIndex(bytes.NewReader(buf.Bytes()))
}

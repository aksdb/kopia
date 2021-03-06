package object

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"sync"

	"github.com/pkg/errors"

	"github.com/kopia/kopia/internal/buf"
	"github.com/kopia/kopia/repo/compression"
	"github.com/kopia/kopia/repo/content"
	"github.com/kopia/kopia/repo/splitter"
)

// Writer allows writing content to the storage and supports automatic deduplication and encryption
// of written data.
type Writer interface {
	io.WriteCloser

	Result() (ID, error)
}

type contentIDTracker struct {
	mu       sync.Mutex
	contents map[content.ID]bool
}

func (t *contentIDTracker) addContentID(contentID content.ID) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.contents == nil {
		t.contents = make(map[content.ID]bool)
	}

	t.contents[contentID] = true
}

func (t *contentIDTracker) contentIDs() []content.ID {
	t.mu.Lock()
	defer t.mu.Unlock()

	result := make([]content.ID, 0, len(t.contents))
	for k := range t.contents {
		result = append(result, k)
	}

	return result
}

type objectWriter struct {
	ctx context.Context
	om  *Manager

	compressor compression.Compressor

	prefix      content.ID
	buf         buf.Buf
	buffer      *bytes.Buffer
	totalLength int64

	currentPosition int64
	indirectIndex   []indirectObjectEntry

	description string

	splitter splitter.Splitter
}

func (w *objectWriter) initBuffer() {
	w.buf = w.om.bufferPool.Allocate(w.splitter.MaxSegmentSize())
	w.buffer = bytes.NewBuffer(w.buf.Data[:0])
}

func (w *objectWriter) Close() error {
	w.buf.Release()

	if w.splitter != nil {
		w.splitter.Close()
	}

	return nil
}

func (w *objectWriter) Write(data []byte) (n int, err error) {
	dataLen := len(data)
	w.totalLength += int64(dataLen)

	for _, d := range data {
		if err := w.buffer.WriteByte(d); err != nil {
			return 0, err
		}

		if w.splitter.ShouldSplit(d) {
			if err := w.flushBuffer(); err != nil {
				return 0, err
			}
		}
	}

	return dataLen, nil
}

func (w *objectWriter) flushBuffer() error {
	length := w.buffer.Len()
	chunkID := len(w.indirectIndex)
	w.indirectIndex = append(w.indirectIndex, indirectObjectEntry{})
	w.indirectIndex[chunkID].Start = w.currentPosition
	w.indirectIndex[chunkID].Length = int64(length)
	w.currentPosition += int64(length)

	b := w.om.bufferPool.Allocate(length)
	defer b.Release()

	compressedBuf := bytes.NewBuffer(b.Data[:0])

	contentBytes, isCompressed, err := maybeCompressedContentBytes(w.compressor, compressedBuf, w.buffer.Bytes())
	if err != nil {
		return errors.Wrap(err, "unable to prepare content bytes")
	}

	contentID, err := w.om.contentMgr.WriteContent(w.ctx, contentBytes, w.prefix)
	w.buffer.Reset()

	if err != nil {
		return errors.Wrapf(err, "error when flushing chunk %d of %s", chunkID, w.description)
	}

	oid := DirectObjectID(contentID)

	if isCompressed {
		oid = Compressed(oid)
	}

	w.indirectIndex[chunkID].Object = oid

	return nil
}

func maybeCompressedContentBytes(comp compression.Compressor, output *bytes.Buffer, input []byte) (data []byte, isCompressed bool, err error) {
	if comp != nil {
		if err := comp.Compress(output, input); err != nil {
			return nil, false, errors.Wrap(err, "compression error")
		}

		if output.Len() < len(input) {
			return output.Bytes(), true, nil
		}
	}

	return input, false, nil
}

func (w *objectWriter) Result() (ID, error) {
	if w.buffer.Len() > 0 || len(w.indirectIndex) == 0 {
		if err := w.flushBuffer(); err != nil {
			return "", err
		}
	}

	if len(w.indirectIndex) == 1 {
		return w.indirectIndex[0].Object, nil
	}

	iw := &objectWriter{
		ctx:         w.ctx,
		om:          w.om,
		compressor:  nil,
		description: "LIST(" + w.description + ")",
		splitter:    w.om.newSplitter(),
		prefix:      w.prefix,
	}

	iw.initBuffer()

	defer iw.Close() //nolint:errcheck

	ind := indirectObject{
		StreamID: "kopia:indirect",
		Entries:  w.indirectIndex,
	}

	if err := json.NewEncoder(iw).Encode(ind); err != nil {
		return "", errors.Wrap(err, "unable to write indirect object index")
	}

	oid, err := iw.Result()
	if err != nil {
		return "", err
	}

	return IndirectObjectID(oid), nil
}

// WriterOptions can be passed to Repository.NewWriter()
type WriterOptions struct {
	Description string
	Prefix      content.ID // empty string or a single-character ('g'..'z')
	Compressor  compression.Name
}

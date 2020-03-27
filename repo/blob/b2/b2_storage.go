// Package b2 implements Storage based on an Backblaze B2 bucket.
package b2

import (
	"context"
	"fmt"
	"io/ioutil"

	"github.com/efarrer/iothrottler"
	"github.com/kurin/blazer/b2"
	"github.com/pkg/errors"

	"github.com/kopia/kopia/internal/iocopy"
	"github.com/kopia/kopia/repo/blob"
)

const (
	b2storageType = "b2"
)

type b2Storage struct {
	Options

	ctx context.Context

	cli    *b2.Client
	bucket *b2.Bucket

	downloadThrottler *iothrottler.IOThrottlerPool
	uploadThrottler   *iothrottler.IOThrottlerPool
}

func (s *b2Storage) GetBlob(ctx context.Context, id blob.ID, offset, length int64) ([]byte, error) {
	obj := s.getObject(id)

	var r *b2.Reader

	if length > 0 {
		r = obj.NewRangeReader(ctx, offset, length)
	} else {
		r = obj.NewReader(ctx)
	}
	defer r.Close() //nolint:errcheck

	throttled, err := s.downloadThrottler.AddReader(r)
	if err != nil {
		return nil, err
	}

	b, err := ioutil.ReadAll(throttled)
	if err != nil {
		return nil, translateError(err)
	}

	if len(b) != int(length) && length > 0 {
		return nil, errors.Errorf("invalid length, got %v bytes, but expected %v", len(id), length)
	}

	if length == 0 {
		return []byte{}, nil
	}

	return b, nil
}

func translateError(err error) error {
	if b2.IsNotExist(err) {
		return blob.ErrBlobNotFound
	}

	return err
}

func (s *b2Storage) PutBlob(ctx context.Context, id blob.ID, data blob.Bytes) error {
	throttled, err := s.uploadThrottler.AddReader(ioutil.NopCloser(data.Reader()))
	if err != nil {
		return err
	}

	progressCallback := blob.ProgressCallback(ctx)
	if progressCallback != nil {
		progressCallback(string(id), 0, int64(data.Length()))
		defer progressCallback(string(id), int64(data.Length()), int64(data.Length()))
	}

	o := s.getObject(id)

	w := o.NewWriter(ctx)
	defer w.Close() //nolint:errcheck

	_, err = iocopy.Copy(w, throttled)

	return translateError(err)
}

func (s *b2Storage) DeleteBlob(ctx context.Context, id blob.ID) error {
	o := s.getObject(id)
	err := o.Delete(ctx)

	return translateError(err)
}

func (s *b2Storage) getObjectNameString(id blob.ID) string {
	return s.Prefix + string(id)
}

func (s *b2Storage) getObject(id blob.ID) *b2.Object {
	return s.bucket.Object(s.getObjectNameString(id))
}

func (s *b2Storage) ListBlobs(ctx context.Context, prefix blob.ID, callback func(blob.Metadata) error) error {
	oi := s.bucket.List(ctx, b2.ListPrefix(s.getObjectNameString(prefix)))
	for oi.Next() {
		o := oi.Object()

		attrs, err := o.Attrs(ctx)
		if err != nil {
			return errors.Wrapf(err, "cannot get attributes of object %q", o.Name())
		}

		bm := blob.Metadata{
			BlobID:    blob.ID(o.Name()[len(s.Prefix):]),
			Length:    attrs.Size,
			Timestamp: attrs.LastModified,
		}

		if err := callback(bm); err != nil {
			return err
		}
	}

	return oi.Err()
}

func (s *b2Storage) ConnectionInfo() blob.ConnectionInfo {
	return blob.ConnectionInfo{
		Type:   b2storageType,
		Config: &s.Options,
	}
}

func (s *b2Storage) Close(ctx context.Context) error {
	return nil
}

func (s *b2Storage) String() string {
	return fmt.Sprintf("b2://%s/%s", s.BucketName, s.Prefix)
}

func toBandwidth(bytesPerSecond int) iothrottler.Bandwidth {
	if bytesPerSecond <= 0 {
		return iothrottler.Unlimited
	}

	return iothrottler.Bandwidth(bytesPerSecond) * iothrottler.BytesPerSecond
}

// New creates new B2-backed storage with specified options:
func New(ctx context.Context, opt *Options) (blob.Storage, error) {
	if opt.BucketName == "" {
		return nil, errors.New("bucket name must be specified")
	}

	cli, err := b2.NewClient(ctx, opt.KeyID, opt.Key)
	if err != nil {
		return nil, errors.Wrap(err, "unable to create client")
	}

	downloadThrottler := iothrottler.NewIOThrottlerPool(toBandwidth(opt.MaxDownloadSpeedBytesPerSecond))
	uploadThrottler := iothrottler.NewIOThrottlerPool(toBandwidth(opt.MaxUploadSpeedBytesPerSecond))

	bucket, err := cli.Bucket(ctx, opt.BucketName)
	if err != nil {
		return nil, errors.Wrapf(err, "cannot open bucket %q", opt.BucketName)
	}

	return &b2Storage{
		Options:           *opt,
		ctx:               ctx,
		cli:               cli,
		bucket:            bucket,
		downloadThrottler: downloadThrottler,
		uploadThrottler:   uploadThrottler,
	}, nil
}

func init() {
	blob.AddSupportedStorage(
		b2storageType,
		func() interface{} {
			return &Options{}
		},
		func(ctx context.Context, o interface{}) (blob.Storage, error) {
			return New(ctx, o.(*Options))
		})
}
